/*
 * MinIO Cloud Storage, (C) 2022 MinIO, Inc.
 * Modifications copyright (c) 2026 DATA.BET
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

package simdjson_benchmarks

import (
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"testing"

	"github.com/buger/jsonparser"
	"github.com/bytedance/sonic"
	"github.com/klauspost/compress/zstd"

	simdjson "github.com/minio/simdjson-go"
)

func benchmarkEncodingJson(b *testing.B, filename string) {
	msg := loadCompressed(b, filename)

	b.SetBytes(int64(len(msg)))
	b.ReportAllocs()
	b.ResetTimer()

	var parsed interface{}
	for i := 0; i < b.N; i++ {
		if err := json.Unmarshal(msg, &parsed); err != nil {
			b.Fatal(err)
		}
	}
}

// benchmarkSonic is the sonic row of the same group as benchmarkEncodingJson: the same
// call, from a []byte, producing the same interface{}.
//
// sonic.Unmarshal is ConfigDefault, which - unlike encoding/json - refers to the input
// for string values rather than copying them, and does not reject a raw control
// character inside a string. BenchmarkMaterialise in samework_test.go is the row that
// matches those semantics up; this one is here to be read next to its neighbour.
func benchmarkSonic(b *testing.B, filename string) {
	if !sonicIsNative {
		b.Skip(sonicNotNative)
	}
	msg := loadCompressed(b, filename)

	var parsed interface{}
	// sonic compiles a decoder on first use. Timing that would charge the first
	// iteration for work no later one repeats.
	if err := sonic.Unmarshal(msg, &parsed); err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(msg)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := sonic.Unmarshal(msg, &parsed); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkSimdJson(b *testing.B, filename string) {
	if !simdjson.SupportedCPU() {
		b.SkipNow()
	}

	msg := loadCompressed(b, filename)

	b.SetBytes(int64(len(msg)))
	b.ReportAllocs()
	b.ResetTimer()

	pj := &simdjson.ParsedJson{}
	for i := 0; i < b.N; i++ {
		// Reset tape
		var err error
		pj, err = simdjson.Parse(msg, pj, simdjson.WithCopyStrings(false))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodingJsonApache_builds(b *testing.B)  { benchmarkEncodingJson(b, "apache_builds") }
func BenchmarkEncodingJsonCanada(b *testing.B)         { benchmarkEncodingJson(b, "canada") }
func BenchmarkEncodingJsonCitm_catalog(b *testing.B)   { benchmarkEncodingJson(b, "citm_catalog") }
func BenchmarkEncodingJsonGithub_events(b *testing.B)  { benchmarkEncodingJson(b, "github_events") }
func BenchmarkEncodingJsonGsoc_2018(b *testing.B)      { benchmarkEncodingJson(b, "gsoc-2018") }
func BenchmarkEncodingJsonInstruments(b *testing.B)    { benchmarkEncodingJson(b, "instruments") }
func BenchmarkEncodingJsonMarine_ik(b *testing.B)      { benchmarkEncodingJson(b, "marine_ik") }
func BenchmarkEncodingJsonMesh(b *testing.B)           { benchmarkEncodingJson(b, "mesh") }
func BenchmarkEncodingJsonMesh_pretty(b *testing.B)    { benchmarkEncodingJson(b, "mesh.pretty") }
func BenchmarkEncodingJsonNumbers(b *testing.B)        { benchmarkEncodingJson(b, "numbers") }
func BenchmarkEncodingJsonRandom(b *testing.B)         { benchmarkEncodingJson(b, "random") }
func BenchmarkEncodingJsonTwitter(b *testing.B)        { benchmarkEncodingJson(b, "twitter") }
func BenchmarkEncodingJsonTwitterescaped(b *testing.B) { benchmarkEncodingJson(b, "twitterescaped") }
func BenchmarkEncodingJsonUpdate_center(b *testing.B)  { benchmarkEncodingJson(b, "update-center") }

func BenchmarkSonicApache_builds(b *testing.B)  { benchmarkSonic(b, "apache_builds") }
func BenchmarkSonicCanada(b *testing.B)         { benchmarkSonic(b, "canada") }
func BenchmarkSonicCitm_catalog(b *testing.B)   { benchmarkSonic(b, "citm_catalog") }
func BenchmarkSonicGithub_events(b *testing.B)  { benchmarkSonic(b, "github_events") }
func BenchmarkSonicGsoc_2018(b *testing.B)      { benchmarkSonic(b, "gsoc-2018") }
func BenchmarkSonicInstruments(b *testing.B)    { benchmarkSonic(b, "instruments") }
func BenchmarkSonicMarine_ik(b *testing.B)      { benchmarkSonic(b, "marine_ik") }
func BenchmarkSonicMesh(b *testing.B)           { benchmarkSonic(b, "mesh") }
func BenchmarkSonicMesh_pretty(b *testing.B)    { benchmarkSonic(b, "mesh.pretty") }
func BenchmarkSonicNumbers(b *testing.B)        { benchmarkSonic(b, "numbers") }
func BenchmarkSonicRandom(b *testing.B)         { benchmarkSonic(b, "random") }
func BenchmarkSonicTwitter(b *testing.B)        { benchmarkSonic(b, "twitter") }
func BenchmarkSonicTwitterescaped(b *testing.B) { benchmarkSonic(b, "twitterescaped") }
func BenchmarkSonicUpdate_center(b *testing.B)  { benchmarkSonic(b, "update-center") }

func BenchmarkSimdJsonApache_builds(b *testing.B)  { benchmarkSimdJson(b, "apache_builds") }
func BenchmarkSimdJsonCanada(b *testing.B)         { benchmarkSimdJson(b, "canada") }
func BenchmarkSimdJsonCitm_catalog(b *testing.B)   { benchmarkSimdJson(b, "citm_catalog") }
func BenchmarkSimdJsonGithub_events(b *testing.B)  { benchmarkSimdJson(b, "github_events") }
func BenchmarkSimdJsonGsoc_2018(b *testing.B)      { benchmarkSimdJson(b, "gsoc-2018") }
func BenchmarkSimdJsonInstruments(b *testing.B)    { benchmarkSimdJson(b, "instruments") }
func BenchmarkSimdJsonMarine_ik(b *testing.B)      { benchmarkSimdJson(b, "marine_ik") }
func BenchmarkSimdJsonMesh(b *testing.B)           { benchmarkSimdJson(b, "mesh") }
func BenchmarkSimdJsonMesh_pretty(b *testing.B)    { benchmarkSimdJson(b, "mesh.pretty") }
func BenchmarkSimdJsonNumbers(b *testing.B)        { benchmarkSimdJson(b, "numbers") }
func BenchmarkSimdJsonRandom(b *testing.B)         { benchmarkSimdJson(b, "random") }
func BenchmarkSimdJsonTwitter(b *testing.B)        { benchmarkSimdJson(b, "twitter") }
func BenchmarkSimdJsonTwitterescaped(b *testing.B) { benchmarkSimdJson(b, "twitterescaped") }
func BenchmarkSimdJsonUpdate_center(b *testing.B)  { benchmarkSimdJson(b, "update-center") }

func BenchmarkBugerJsonParserLarge(b *testing.B) {
	largeFixture := loadCompressed(b, "payload-large")
	const logVals = false
	b.SetBytes(int64(len(largeFixture)))
	b.ReportAllocs()
	b.ResetTimer()
	var dump int
	for i := 0; i < b.N; i++ {
		jsonparser.ArrayEach(largeFixture, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
			sval, _, _, _ := jsonparser.Get(value, "username")
			if logVals && i == 0 {
				b.Log(string(sval))
			}
			dump += len(sval)
		}, "users")

		jsonparser.ArrayEach(largeFixture, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
			ival, _ := jsonparser.GetInt(value, "id")
			if logVals && i == 0 {
				b.Log(ival)
			}
			dump += int(ival)
			sval, _, _, _ := jsonparser.Get(value, "slug")
			if logVals && i == 0 {
				b.Log(string(sval))
			}
			dump += len(sval)
		}, "topics", "topics")
	}
	if dump == 0 {
		b.Log("")
	}
}

// tester and loadCompressed should be kept in sync with minio/simdjson-go/parsed_json_test.go.
type tester interface {
	Fatal(args ...interface{})
}

func loadCompressed(t tester, file string) (ref []byte) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, err = ioutil.ReadFile(filepath.Join("../", "testdata", file+".json.zst"))
	if err != nil {
		t.Fatal(err)
	}
	ref, err = dec.DecodeAll(ref, nil)
	if err != nil {
		t.Fatal(err)
	}

	return ref
}
