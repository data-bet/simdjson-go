//go:build ignore

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

// Command gen_tiny_floats writes testdata/tiny-floats.json.zst. This synthetic document holds
// mostly float64 values far below one, in the form that encoding/json writes: tokens with an
// exponent, and fractions with leading zeros, with 15 to 17 significant digits. The number
// parser uses its longest paths for these tokens, and the other corpora contain almost none
// of them. Run the command from the root of the repository:
//
//	go run testdata/gen_tiny_floats.go
//
// The seed is fixed, so each run writes the same JSON. The compressed bytes can change with the
// version of klauspost/compress. TestTinyFloatsCorpusMix checks the result.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const (
	seed    = 20261110
	items   = 165
	rowLen  = 12
	outPath = "testdata/tiny-floats.json.zst"
)

// edge holds number shapes that encoding/json does not write, but that a parser must read
// correctly. It is raw text, because marshaling would normalize each of them.
const edge = `{"negative_zero":-0.0,"upper_e":3.141592653589793E-12,` +
	`"plus_exponent":6.02214076e+23,"integer_exponent":1e5,` +
	`"max":1.7976931348623157e+308,"subnormal":4.9406564584124654e-324,` +
	`"digits19":9007199254740993.001,"digits20":9007199254740993.0001}`

type document struct {
	Name  string          `json:"name"`
	Seed  int             `json:"seed"`
	Edge  json.RawMessage `json:"edge"`
	Items []item          `json:"items"`
}

type item struct {
	ID     int      `json:"id"`
	Name   string   `json:"name"`
	Group  string   `json:"group"`
	Series []series `json:"series"`
}

type series struct {
	ID      int         `json:"id"`
	Name    string      `json:"name"`
	X       float64     `json:"x"`
	Labels  []string    `json:"labels"`
	Samples [][]float64 `json:"samples"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	r := rand.New(rand.NewPCG(seed, seed))
	doc := document{Name: "synthetic", Seed: seed, Edge: json.RawMessage(edge)}
	seriesID := 1
	for i := range items {
		it := item{
			ID:    100000 + i,
			Name:  fmt.Sprintf("item-%05d", i),
			Group: fmt.Sprintf("group-%d", r.IntN(6)),
		}
		for range 4 + r.IntN(7) {
			it.Series = append(it.Series, newSeries(r, seriesID))
			seriesID++
		}
		doc.Items = append(doc.Items, it)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal document: %w", err)
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return fmt.Errorf("create zstd encoder: %w", err)
	}
	defer enc.Close()
	packed := enc.EncodeAll(raw, nil)
	if err := os.WriteFile(outPath, packed, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("%s: %d bytes of JSON, %d compressed\n", outPath, len(raw), len(packed))
	return nil
}

func newSeries(r *rand.Rand, id int) series {
	labels := []string{"a", "b", "c"}
	s := series{
		ID:      id,
		Name:    fmt.Sprintf("series-%05d", id),
		X:       float64(r.IntN(40)) + 0.5,
		Labels:  labels,
		Samples: make([][]float64, len(labels)),
	}
	for i := range s.Samples {
		row := make([]float64, rowLen)
		for j := range row {
			row[j] = sample(r)
		}
		s.Samples[i] = row
	}
	return s
}

// sample returns one entry. The weights are per mille. With the ids and the x values, they give
// the mix of number tokens that TestTinyFloatsCorpusMix expects.
func sample(r *rand.Rand) float64 {
	switch w := r.IntN(1000); {
	case w < 530:
		return long(r, 7+r.IntN(14)) // [1e-20, 1e-6): written with an exponent
	case w < 955:
		return long(r, 3+r.IntN(4)) // [1e-6, 1e-2): written as 0.00…
	case w < 980:
		return float64(1+r.IntN(999)) / 1000
	default:
		return float64(r.IntN(2))
	}
}

// long returns a value in [1e-k, 1e-(k-1)) whose shortest form has 15 to 17 significant digits.
// When a value has a shorter form, which is rare, long draws a new value.
func long(r *rand.Rand, k int) float64 {
	for {
		// The compiler fuses 1 + 9*x into one FMA instruction on arm64 but not on amd64,
		// and the results differ in the last bit. math.FMA gives the same file on all
		// architectures.
		f := math.FMA(9, r.Float64(), 1) / math.Pow10(k)
		mant, _, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
		if digits := len(mant) - 1; digits >= 15 {
			return f
		}
	}
}
