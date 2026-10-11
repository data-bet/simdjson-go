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

package simdjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// DeleteElems, SetNull and Deserialize write NOPs, and each reader must skip them. The
// expected values come from encoding/json, not from this package. MarshalJSON and
// Object.Map read NOPs correctly before this test, and they are the control.

func deleteFromArray(index ...int) func(t *testing.T, root Iter) {
	return func(t *testing.T, root Iter) {
		a, err := root.Array(nil)
		if err != nil {
			t.Fatal(err)
		}
		n := -1
		a.DeleteElems(func(Iter) bool {
			n++
			for _, k := range index {
				if k == n {
					return true
				}
			}
			return false
		})
	}
}

func deleteFromObject(keys ...string) func(t *testing.T, root Iter) {
	return func(t *testing.T, root Iter) {
		o, err := root.Object(nil)
		if err != nil {
			t.Fatal(err)
		}
		only := map[string]struct{}{}
		for _, k := range keys {
			only[k] = struct{}{}
		}
		if err := o.DeleteElems(nil, only); err != nil {
			t.Fatal(err)
		}
	}
}

func setNullInArray(index int) func(t *testing.T, root Iter) {
	return func(t *testing.T, root Iter) {
		a, err := root.Array(nil)
		if err != nil {
			t.Fatal(err)
		}
		n := -1
		a.ForEach(func(i Iter) {
			if n++; n == index {
				if err := i.SetNull(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

var nopSkipCases = []struct {
	json, want string
	edit       func(t *testing.T, root Iter)
}{
	{`[1,2,3,4]`, `[1,3,4]`, deleteFromArray(1)},
	{`[1,2,3,4]`, `[2,3,4]`, deleteFromArray(0)},
	{`[1,2,3,4]`, `[1,2,3]`, deleteFromArray(3)},
	{`[1,2,3,4]`, `[1,4]`, deleteFromArray(1, 2)},
	{`[true,false,null,true]`, `[true,null,true]`, deleteFromArray(1)},
	{`["a","b","c"]`, `["a","c"]`, deleteFromArray(1)},
	{`[1,[2,3],{"x":4},5]`, `[1,5]`, deleteFromArray(1, 2)},
	{`[1.5,2,3]`, `[null,2,3]`, setNullInArray(0)},
	{`["s",2,3]`, `[null,2,3]`, setNullInArray(0)},
	{`[[1,2],3,4]`, `[null,3,4]`, setNullInArray(0)},
	{`[{"a":[1,2]},3]`, `[null,3]`, setNullInArray(0)},
	{`{"a":1,"b":2,"c":3}`, `{"a":1,"c":3}`, deleteFromObject("b")},
	{`{"a":true,"b":false,"c":null}`, `{"a":true,"c":null}`, deleteFromObject("b")},
	{`{"a":1,"b":[1,2],"c":{"d":3},"e":4}`, `{"a":1,"e":4}`, deleteFromObject("b", "c")},
}

var errNotFor = errors.New("not for this kind of value")

var nopReaders = []struct {
	name string
	read func(pj *ParsedJson, root Iter) (interface{}, error)
}{
	{"MarshalJSON", func(pj *ParsedJson, _ Iter) (interface{}, error) {
		it := pj.Iter()
		b, err := it.MarshalJSON()
		if err != nil {
			return nil, err
		}
		var v interface{}
		err = json.Unmarshal(b, &v)
		return v, err
	}},
	{"Object.Map", func(_ *ParsedJson, root Iter) (interface{}, error) {
		if root.Type() != TypeObject {
			return nil, errNotFor
		}
		return mapOf(root)
	}},
	{"Iter.Interface", func(_ *ParsedJson, root Iter) (interface{}, error) {
		return root.Interface()
	}},
	{"ForEach", func(_ *ParsedJson, root Iter) (interface{}, error) {
		if root.Type() == TypeArray {
			a, err := root.Array(nil)
			if err != nil {
				return nil, err
			}
			out := []interface{}{}
			a.ForEach(func(i Iter) {
				v, e := i.Interface()
				if e != nil && err == nil {
					err = e
				}
				out = append(out, v)
			})
			return out, err
		}
		o, err := root.Object(nil)
		if err != nil {
			return nil, err
		}
		out := map[string]interface{}{}
		err = o.ForEach(func(key []byte, i Iter) {
			out[string(key)], _ = i.Interface()
		}, nil)
		return out, err
	}},
	{"Advance", func(_ *ParsedJson, root Iter) (interface{}, error) {
		var it Iter
		if root.Type() == TypeArray {
			a, err := root.Array(nil)
			if err != nil {
				return nil, err
			}
			it = a.Iter()
		} else {
			o, err := root.Object(nil)
			if err != nil {
				return nil, err
			}
			it = o.tape.Iter()
			it.off = o.off
		}
		out := []interface{}{}
		for it.Advance() != TypeNone {
			v, err := it.Interface()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		if root.Type() == TypeArray {
			return out, nil
		}
		m := map[string]interface{}{}
		for k := 0; k+1 < len(out); k += 2 {
			m[out[k].(string)] = out[k+1]
		}
		return m, nil
	}},
	{"AdvanceIter", func(_ *ParsedJson, root Iter) (interface{}, error) {
		var it Iter
		if root.Type() == TypeArray {
			a, err := root.Array(nil)
			if err != nil {
				return nil, err
			}
			it = a.Iter()
		} else {
			o, err := root.Object(nil)
			if err != nil {
				return nil, err
			}
			it = o.tape.Iter()
			it.off = o.off
		}
		out := []interface{}{}
		var elem Iter
		for {
			typ, err := it.AdvanceIter(&elem)
			if err != nil {
				return nil, err
			}
			if typ == TypeNone {
				break
			}
			v, err := elem.Interface()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		if root.Type() == TypeArray {
			return out, nil
		}
		m := map[string]interface{}{}
		for k := 0; k+1 < len(out); k += 2 {
			m[out[k].(string)] = out[k+1]
		}
		return m, nil
	}},
	{"Array.MarshalJSON", func(_ *ParsedJson, root Iter) (interface{}, error) {
		if root.Type() != TypeArray {
			return nil, errNotFor
		}
		a, err := root.Array(nil)
		if err != nil {
			return nil, err
		}
		b, err := a.MarshalJSON()
		if err != nil {
			return nil, err
		}
		var v interface{}
		err = json.Unmarshal(b, &v)
		return v, err
	}},
	{"FindKey", func(_ *ParsedJson, root Iter) (interface{}, error) {
		if root.Type() != TypeObject {
			return nil, errNotFor
		}
		// FindKey uses Advance and AdvanceIter. Map uses neither, and gives the keys.
		// Because Map moves its Object to the end, FindKey needs a different Object.
		keys, err := mapOf(root)
		if err != nil {
			return nil, err
		}
		o, err := root.Object(nil)
		if err != nil {
			return nil, err
		}
		out := map[string]interface{}{}
		for k := range keys {
			e := o.FindKey(k, nil)
			if e == nil {
				return nil, fmt.Errorf("FindKey(%q) found nothing", k)
			}
			if out[k], err = e.Iter.Interface(); err != nil {
				return nil, err
			}
		}
		return out, nil
	}},
}

func mapOf(root Iter) (map[string]interface{}, error) {
	o, err := root.Object(nil)
	if err != nil {
		return nil, err
	}
	return o.Map(nil)
}

func rootValue(t *testing.T, pj *ParsedJson) Iter {
	t.Helper()
	var root Iter
	found := false
	if err := pj.ForEach(func(i Iter) error {
		root, found = i, true
		return nil
	}); err != nil || !found {
		t.Fatalf("no root value: %v", err)
	}
	return root
}

// Interface gives int64 and uint64, but encoding/json gives float64.
func normalize(v interface{}) interface{} {
	switch x := v.(type) {
	case []interface{}:
		for k := range x {
			x[k] = normalize(x[k])
		}
	case map[string]interface{}:
		for k := range x {
			x[k] = normalize(x[k])
		}
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	}
	return v
}

func TestReadersSkipNOPs(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	for _, tc := range nopSkipCases {
		var want interface{}
		if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
			t.Fatal(err)
		}
		for _, roundTrip := range []bool{false, true} {
			pj, err := Parse([]byte(tc.json), nil)
			if err != nil {
				t.Fatal(err)
			}
			tc.edit(t, rootValue(t, pj))
			if roundTrip {
				// Deserialize writes the NOPs again, from its own count.
				s := NewSerializer()
				if pj, err = s.Deserialize(s.Serialize(nil, *pj), nil); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range nopReaders {
				got, err := r.read(pj, rootValue(t, pj))
				if err == errNotFor {
					continue
				}
				if err != nil {
					t.Errorf("%s -> %s, round trip %v, %s: %v", tc.json, tc.want, roundTrip, r.name, err)
					continue
				}
				if got = normalize(got); !reflect.DeepEqual(got, want) {
					t.Errorf("%s -> %s, round trip %v, %s: got %v", tc.json, tc.want, roundTrip, r.name, got)
				}
			}
		}
	}
}

// A skip of 0 comes back to the same NOP. Each reader must stop there, and must not
// continue in a loop.
func TestReadersStopAtZeroNOP(t *testing.T) {
	pj := &ParsedJson{Tape: []uint64{word(TagNull, 0), word(TagNop, 0), word(TagBoolTrue, 0)}}

	i := pj.Iter()
	if typ := i.Advance(); typ != TypeNull {
		t.Fatalf("Advance: %v, want null", typ)
	}
	if typ := i.Advance(); typ != TypeNone || i.t != TagEnd {
		t.Errorf("Advance: %v, tag %v, want the end", typ, i.t)
	}

	i = pj.Iter()
	if tag := i.AdvanceInto(); tag != TagNull {
		t.Fatalf("AdvanceInto: %v, want null", tag)
	}
	if tag := i.AdvanceInto(); tag != TagEnd {
		t.Errorf("AdvanceInto: %v, want the end", tag)
	}

	i = pj.Iter()
	var dst Iter
	if typ, err := i.AdvanceIter(&dst); typ != TypeNull || err != nil {
		t.Fatalf("AdvanceIter: %v, %v, want null", typ, err)
	}
	if _, err := i.AdvanceIter(&dst); err == nil {
		t.Error("AdvanceIter: no error")
	}
}
