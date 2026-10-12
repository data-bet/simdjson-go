/*
 * Copyright (c) 2026 DATA.BET
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// This file holds the comparisons where every library is asked for the same thing.
//
// The older benchmarks in benchmarks_test.go are not that. simdjson.Parse builds a tape
// and stops; encoding/json and sonic build a Go value out of every element in the
// document. Both numbers are real, but the ratio between them is not a parser
// comparison, and reusing the same *ParsedJson across iterations means simdjson's row
// is also the only one measured warm.
//
// Two groups here, each a workload rather than a call:
//
//   - BenchmarkMaterialise: the caller wants the whole document as Go values. Every
//     library returns an interface{} tree that owns its strings.
//   - BenchmarkExtract: the caller wants three fields out of a document. Every
//     library may use whatever API is fastest for it, including skipping the rest of
//     the input, and TestExtractAgree checks they all came back with the same answer.
//
// What each row is allowed to differ in, and what was equalised:
//
//   - Strings. simdjson with WithCopyStrings(false) points into the message, but
//     Iter.Interface() goes through stringAt, which copies, so the materialise row
//     copies each string exactly once - as encoding/json does. Passing
//     WithCopyStrings(true) would copy twice. sonic refers to the input unless
//     CopyString is set, so the materialise row uses ConfigStd, which sets it.
//   - Rejecting a raw control character inside a string. simdjson always does; sonic
//     only with ValidateString, which ConfigStd also sets. ConfigDefault does not.
//   - Validating UTF-8. simdjson does not unless WithValidateUTF8(true) is passed,
//     and sonic never does. Left off on both sides, so this is matched by accident
//     rather than by choice; turning it on for simdjson costs up to 19%. jsontext
//     validates by default, so its row sets AllowInvalidUTF8(true).
//   - Duplicate object names. jsontext rejects them by default, and to do that it
//     records every name of every open object. No other library here checks for
//     them, so the jsontext row sets AllowDuplicateNames(true).
//   - Copying the input. sonic.Unmarshal([]byte) is UnmarshalFromString(string(buf)),
//     which copies the whole payload on every call - 2.2 MB per operation on canada.
//     Both are measured: "sonic" pays it, "sonic_str" is handed a string made once.
//   - Cores. simdjson runs stage 1 and stage 2 in separate goroutines once a message
//     is over 256 KB, which is 10 of the 14 corpus files, while every other library
//     here is single-threaded. Sequential ns/op therefore compares one library's
//     wall clock on two cores against another's on one. The "par" sub-benchmark is
//     the answer: with every core already busy, overlapping the two stages buys
//     nothing, so it measures CPU spent rather than latency achieved.
//   - Warm-up. sonic compiles a decoder per type on first use, and simdjson grows
//     its tape and index buffers on the first parse. Every row runs its closure once
//     before the timer starts, and every row gets its own reusable state.
//   - GC. simdjson allocates almost nothing per operation and the others allocate a
//     tree; a collection triggered by one row can be charged to whoever runs next.
//     runtime.GC() runs before each timer starts. That bounds the damage; it does
//     not eliminate it, so read allocs/op alongside ns/op rather than instead of it.
//
// Libraries are sub-benchmarks under one parent so that -count=N interleaves them
// round by round instead of running all of one and then all of the next. Their names
// are key=value because that is what lets benchstat pivot one axis against another
// rather than printing one row per name:
//
//	go test -run '^$' -bench BenchmarkMaterialise -benchtime 2s -count 10 >mat.txt
//	benchstat -col /lib -row /file -filter '/mode:seq' mat.txt
//
// Do not shorten -benchtime much below a second. Every row but simdjson's allocates
// a tree per operation, so the sequential rows are partly measuring the concurrent
// collector on the other cores, and the framework's own choice of iteration count is
// made from a first measurement that the same noise distorts. At -benchtime 200ms
// this file reported 715 us/op for sonic on github_events where a fixed -benchtime
// 2000x reports 110 us/op, a factor of six from nothing but the ramp. Repeated rounds
// and a median are not optional here.
//
// Pin the sequential rows. On the machine this was written on, BenchmarkExtract over
// six rounds reported ±73% for simdjson and ±59% for sonic_ast unpinned, and ±3% and
// ±4% under taskset on one core, with the medians moving by about 1%. The spread was
// the scheduler moving rows between cores that do not hold the same clock, not the
// libraries. Two cores at least once the input passes 256 KB, or the pin serialises
// the two goroutines simdjson starts there and the row measures a handicap:
//
//	taskset -c 2,3 go test -run '^$' -bench ... -count 10
//
// The par rows want every core instead, so run them unpinned and separately.
//
// GOGC=off (with a GOMEMLIMIT that fits the run) answers a different and narrower
// question: how fast each library fills memory, with nobody reclaiming it. Worth
// having next to the default, never instead of it - the allocation these rows do is
// part of what they cost, and the B/op and allocs/op columns are the ones that say so.
package simdjson_benchmarks

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/buger/jsonparser"
	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"

	simdjson "github.com/data-bet/simdjson-go"
)

// sink keeps a result reachable so that no row can be optimised away for producing
// nothing anybody reads. Sequential rows assign to it directly; a parallel row keeps
// its own and hands it over once at the end, since assigning an interface from several
// goroutines is a race, and -race would rightly say so.
var (
	sinkMu sync.Mutex
	sink   interface{}
)

func keep(v interface{}) {
	sinkMu.Lock()
	sink = v
	sinkMu.Unlock()
}

// corpus is the file set the older benchmarks in this package use, so the two can be
// read against each other.
var corpus = []string{
	"apache_builds", "canada", "citm_catalog", "github_events", "gsoc-2018",
	"instruments", "marine_ik", "mesh", "mesh.pretty", "numbers", "random",
	"twitter", "twitterescaped", "update-center",
}

// materialiser names one way of turning a document into Go values. newRun returns a
// closure holding whatever per-caller state that library reuses between calls, so
// that a parallel run gives each goroutine its own.
type materialiser struct {
	name   string
	newRun func() func(msg []byte, str string) (interface{}, error)
}

var materialisers = []materialiser{{
	name: "encoding_json",
	newRun: func() func([]byte, string) (interface{}, error) {
		return func(msg []byte, _ string) (interface{}, error) {
			var v interface{}
			return v, json.Unmarshal(msg, &v)
		}
	},
}, {
	name: "sonic",
	newRun: func() func([]byte, string) (interface{}, error) {
		return func(msg []byte, _ string) (interface{}, error) {
			var v interface{}
			return v, sonic.ConfigStd.Unmarshal(msg, &v)
		}
	},
}, {
	// The same work as the row above, minus the copy of the whole payload that
	// sonic.Unmarshal makes because its decoder takes a string.
	name: "sonic_str",
	newRun: func() func([]byte, string) (interface{}, error) {
		return func(_ []byte, str string) (interface{}, error) {
			var v interface{}
			return v, sonic.ConfigStd.UnmarshalFromString(str, &v)
		}
	},
}, {
	name: "simdjson",
	newRun: func() func([]byte, string) (interface{}, error) {
		pj := &simdjson.ParsedJson{}
		return func(msg []byte, _ string) (interface{}, error) {
			var err error
			pj, err = simdjson.Parse(msg, pj, simdjson.WithCopyStrings(false))
			if err != nil {
				return nil, err
			}
			iter := pj.Iter()
			return iter.Interface()
		}
	},
}}

func BenchmarkMaterialise(b *testing.B) {
	for _, file := range corpus {
		b.Run("file="+file, func(b *testing.B) {
			msg := loadCompressed(b, file)
			str := string(msg)
			for _, m := range materialisers {
				b.Run("lib="+m.name, func(b *testing.B) {
					if !simdjson.SupportedCPU() && m.name == "simdjson" {
						b.Skip("no SIMD support on this CPU")
					}
					b.Run("mode=seq", func(b *testing.B) {
						run := m.newRun()
						warm(b, len(msg), func() error {
							v, err := run(msg, str)
							sink = v
							return err
						})
						for i := 0; i < b.N; i++ {
							v, err := run(msg, str)
							if err != nil {
								b.Fatal(err)
							}
							sink = v
						}
					})
					b.Run("mode=par", func(b *testing.B) {
						warm(b, len(msg), func() error {
							v, err := m.newRun()(msg, str)
							sink = v
							return err
						})
						b.RunParallel(func(pb *testing.PB) {
							run := m.newRun()
							var last interface{}
							for pb.Next() {
								v, err := run(msg, str)
								if err != nil {
									// Not b.Fatal: that has to come from the
									// goroutine running the benchmark.
									b.Error(err)
									return
								}
								last = v
							}
							keep(last)
						})
					})
				})
			}
		})
	}
}

// extractDoc is the shape a caller that wants only these fields would declare, which
// is what encoding/json and sonic's struct decoder are given. The lazy readers are
// handed the same three paths.
type extractDoc struct {
	Users []struct {
		Username string `json:"username"`
	} `json:"users"`
	Topics struct {
		Topics []struct {
			ID   int64  `json:"id"`
			Slug string `json:"slug"`
		} `json:"topics"`
	} `json:"topics"`
}

// extractor reads users[].username, topics.topics[].id and topics.topics[].slug and
// returns the sum of the ids and of the lengths of the strings. The checksum is what
// makes the row comparable: TestExtractAgree fails if two libraries disagree, which
// is the only thing standing between this benchmark and a row that quietly finds
// nothing and wins.
type extractor struct {
	name   string
	newRun func() func(msg []byte, str string) (int64, error)
}

// jsontextOpts are the options of the jsontext row. The header comment of this file
// gives the reason for each.
var jsontextOpts = []jsontext.Options{
	jsontext.AllowInvalidUTF8(true),
	jsontext.AllowDuplicateNames(true),
}

// jsontextReader is the state that the jsontext row of BenchmarkExtract keeps between
// calls. The decoder reads from a bytes.Buffer because that is the one reader it does
// not copy into a buffer of its own.
type jsontextReader struct {
	dec jsontext.Decoder
	in  bytes.Buffer
	buf []byte
}

// str reads a string and returns it unescaped. The result is valid until the next
// call.
func (r *jsontextReader) str() ([]byte, error) {
	v, err := r.dec.ReadValue()
	if err != nil {
		return nil, err
	}
	r.buf, err = jsontext.AppendUnquote(r.buf[:0], v)
	return r.buf, err
}

// open reads the token that starts an object or an array.
func (r *jsontextReader) open(kind jsontext.Kind) error {
	tok, err := r.dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != kind {
		return fmt.Errorf("jsontext: want %v, got %v", kind, tok.Kind())
	}
	return nil
}

// more reads the end token if the object or array has no more members. PeekKind
// returns 0 on a syntax error, so the error comes from the next read instead.
func (r *jsontextReader) more() (bool, error) {
	if k := r.dec.PeekKind(); k != '}' && k != ']' {
		return true, nil
	}
	_, err := r.dec.ReadToken()
	return false, err
}

// object calls member with each name of an object. member must read the value.
func (r *jsontextReader) object(member func(name []byte) error) error {
	if err := r.open('{'); err != nil {
		return err
	}
	for {
		more, err := r.more()
		if err != nil || !more {
			return err
		}
		name, err := r.str()
		if err != nil {
			return err
		}
		if err = member(name); err != nil {
			return err
		}
	}
}

// array calls elem once for each element of an array. elem must read the element.
func (r *jsontextReader) array(elem func() error) error {
	if err := r.open('['); err != nil {
		return err
	}
	for {
		more, err := r.more()
		if err != nil || !more {
			return err
		}
		if err = elem(); err != nil {
			return err
		}
	}
}

func (r *jsontextReader) extract(msg []byte) (int64, error) {
	r.in = *bytes.NewBuffer(msg)
	r.dec.Reset(&r.in, jsontextOpts...)
	var sum int64
	strLen := func() error {
		s, err := r.str()
		sum += int64(len(s))
		return err
	}
	err := r.object(func(name []byte) error {
		switch string(name) {
		case "users":
			return r.array(func() error {
				return r.object(func(name []byte) error {
					if string(name) == "username" {
						return strLen()
					}
					return r.dec.SkipValue()
				})
			})
		case "topics":
			return r.object(func(name []byte) error {
				if string(name) != "topics" {
					return r.dec.SkipValue()
				}
				return r.array(func() error {
					return r.object(func(name []byte) error {
						switch string(name) {
						case "id":
							tok, err := r.dec.ReadToken()
							if err != nil {
								return err
							}
							id, err := tok.Int()
							sum += id
							return err
						case "slug":
							return strLen()
						}
						return r.dec.SkipValue()
					})
				})
			})
		}
		return r.dec.SkipValue()
	})
	return sum, err
}

var extractors = []extractor{{
	name: "encoding_json",
	newRun: func() func([]byte, string) (int64, error) {
		return func(msg []byte, _ string) (int64, error) {
			var doc extractDoc
			if err := json.Unmarshal(msg, &doc); err != nil {
				return 0, err
			}
			return doc.sum(), nil
		}
	},
}, {
	name: "sonic_struct",
	newRun: func() func([]byte, string) (int64, error) {
		return func(_ []byte, str string) (int64, error) {
			var doc extractDoc
			if err := sonic.ConfigStd.UnmarshalFromString(str, &doc); err != nil {
				return 0, err
			}
			return doc.sum(), nil
		}
	},
}, {
	// sonic's lazy reader. NewSearcher validates the parts it skips, which is why it
	// is left at its default here: simdjson has walked the whole document by the time
	// it answers, so a searcher that skipped without checking would be reading less
	// JSON, not reading it faster. GetByPath starts each search at the start of the
	// document, so one searcher is sufficient for the two paths, and each path scans
	// again from the start, as jsonparser below does.
	name: "sonic_ast",
	newRun: func() func([]byte, string) (int64, error) {
		return func(_ []byte, str string) (int64, error) {
			var sum int64
			searcher := ast.NewSearcher(str)
			users, err := searcher.GetByPath("users")
			if err != nil {
				return 0, err
			}
			if err = users.ForEach(func(_ ast.Sequence, node *ast.Node) bool {
				name, err := node.Get("username").String()
				if err == nil {
					sum += int64(len(name))
				}
				return true
			}); err != nil {
				return 0, err
			}
			topics, err := searcher.GetByPath("topics", "topics")
			if err != nil {
				return 0, err
			}
			if err = topics.ForEach(func(_ ast.Sequence, node *ast.Node) bool {
				if id, err := node.Get("id").Int64(); err == nil {
					sum += id
				}
				if slug, err := node.Get("slug").String(); err == nil {
					sum += int64(len(slug))
				}
				return true
			}); err != nil {
				return 0, err
			}
			return sum, nil
		}
	},
}, {
	// A streaming reader: it reads the document token by token and skips each value
	// it does not need. SkipValue still checks the syntax of what it skips, as
	// NewSearcher does for sonic_ast.
	name: "jsontext",
	newRun: func() func([]byte, string) (int64, error) {
		r := &jsontextReader{}
		return func(msg []byte, _ string) (int64, error) {
			return r.extract(msg)
		}
	},
}, {
	name: "jsonparser",
	newRun: func() func([]byte, string) (int64, error) {
		return func(msg []byte, _ string) (int64, error) {
			var sum int64
			jsonparser.ArrayEach(msg, func(value []byte, _ jsonparser.ValueType, _ int, _ error) {
				name, _, _, _ := jsonparser.Get(value, "username")
				sum += int64(len(name))
			}, "users")
			jsonparser.ArrayEach(msg, func(value []byte, _ jsonparser.ValueType, _ int, _ error) {
				id, _ := jsonparser.GetInt(value, "id")
				sum += id
				slug, _, _, _ := jsonparser.Get(value, "slug")
				sum += int64(len(slug))
			}, "topics", "topics")
			return sum, nil
		}
	},
}, {
	name: "simdjson",
	newRun: func() func([]byte, string) (int64, error) {
		pj := &simdjson.ParsedJson{}
		var elem *simdjson.Element
		var ar *simdjson.Array
		var obj *simdjson.Object
		onlyKeys := map[string]struct{}{"id": {}, "slug": {}}
		return func(msg []byte, _ string) (int64, error) {
			var sum int64
			var err error
			if pj, err = simdjson.Parse(msg, pj, simdjson.WithCopyStrings(false)); err != nil {
				return 0, err
			}
			iter := pj.Iter()
			if elem, err = iter.FindElement(elem, "users"); err != nil {
				return 0, err
			}
			if ar, err = elem.Iter.Array(ar); err != nil {
				return 0, err
			}
			ar.ForEach(func(i simdjson.Iter) {
				name, err := i.FindElement(nil, "username")
				if err == nil {
					if s, err := name.Iter.StringBytes(); err == nil {
						sum += int64(len(s))
					}
				}
			})
			if elem, err = iter.FindElement(elem, "topics", "topics"); err != nil {
				return 0, err
			}
			if ar, err = elem.Iter.Array(ar); err != nil {
				return 0, err
			}
			ar.ForEach(func(i simdjson.Iter) {
				if obj, err = i.Object(obj); err != nil {
					return
				}
				obj.ForEach(func(key []byte, i simdjson.Iter) {
					switch string(key) {
					case "id":
						if v, err := i.Int(); err == nil {
							sum += v
						}
					case "slug":
						if s, err := i.StringBytes(); err == nil {
							sum += int64(len(s))
						}
					}
				}, onlyKeys)
			})
			return sum, nil
		}
	},
}}

func (d extractDoc) sum() int64 {
	var sum int64
	for _, u := range d.Users {
		sum += int64(len(u.Username))
	}
	for _, t := range d.Topics.Topics {
		sum += t.ID + int64(len(t.Slug))
	}
	return sum
}

const extractFile = "payload-large"

func BenchmarkExtract(b *testing.B) {
	msg := loadCompressed(b, extractFile)
	str := string(msg)
	for _, e := range extractors {
		b.Run("lib="+e.name, func(b *testing.B) {
			if !simdjson.SupportedCPU() && e.name == "simdjson" {
				b.Skip("no SIMD support on this CPU")
			}
			run := e.newRun()
			warm(b, len(msg), func() error {
				v, err := run(msg, str)
				sink = v
				return err
			})
			for i := 0; i < b.N; i++ {
				v, err := run(msg, str)
				if err != nil {
					b.Fatal(err)
				}
				sink = v
			}
		})
	}
}

// TestExtractAgree is what lets BenchmarkExtract be read as a comparison. Without it
// a row that returns early, finds no elements or silently swallows an error is the
// fastest one.
func TestExtractAgree(t *testing.T) {
	msg := loadCompressed(t, extractFile)
	str := string(msg)

	var want int64
	var wantFrom string
	for _, e := range extractors {
		if !simdjson.SupportedCPU() && e.name == "simdjson" {
			t.Logf("%s: skipped, no SIMD support on this CPU", e.name)
			continue
		}
		got, err := e.newRun()(msg, str)
		if err != nil {
			t.Fatalf("%s: %v", e.name, err)
		}
		if got == 0 {
			t.Fatalf("%s: checksum is zero, so it found nothing", e.name)
		}
		t.Logf("%-14s %d", e.name, got)
		if wantFrom == "" {
			want, wantFrom = got, e.name
			continue
		}
		if got != want {
			t.Errorf("%s: checksum %d, but %s got %d", e.name, got, wantFrom, want)
		}
	}
	if wantFrom == "" {
		t.Skip("no extractor could run here")
	}
}

// warm runs one operation outside the timer and collects what it allocated, so that
// neither a lazily built decoder nor another row's garbage lands on this row's clock.
// n is the size of the input, for MB/s.
func warm(b *testing.B, n int, once func() error) {
	b.Helper()
	if err := once(); err != nil {
		b.Fatal(err)
	}
	runtime.GC()
	b.SetBytes(int64(n))
	b.ReportAllocs()
	b.ResetTimer()
}
