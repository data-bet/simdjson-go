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

package main

import (
	"fmt"
	"strings"
	"testing"
)

// bench renders one group's go test output: for each benchmark, one line per sample,
// with ns/op scaled from 1000 by the given factors and fixed allocation figures.
func bench(t *testing.T, benches map[string][]float64, allocs int) *samples {
	t.Helper()
	var b strings.Builder
	b.WriteString("goos: linux\ngoarch: amd64\ncpu: Test CPU\ngoversion: go1.26.0\n")
	for name, factors := range benches {
		for _, f := range factors {
			fmt.Fprintf(&b, "Benchmark%s \t 1000\t %.1f ns/op\t 64 B/op\t %d allocs/op\n", name, 1000*f, allocs)
		}
	}
	s, err := read(strings.NewReader(b.String()), "test")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// twelve returns twelve samples spread by ±spread around factor, the shape ab.sh's
// defaults produce.
func twelve(factor, spread float64) []float64 {
	v := make([]float64, 12)
	for i := range v {
		v[i] = factor * (1 + spread*(float64(i%5)-2)/2)
	}
	return v
}

// eight returns eight benchmarks A to H with samples v, with A's replaced by a if given.
func eight(v, a []float64) map[string][]float64 {
	m := map[string][]float64{}
	for _, name := range strings.Split("ABCDEFGH", "") {
		m[name] = v
	}
	if a != nil {
		m["A"] = a
	}
	return m
}

func TestJudge(t *testing.T) {
	flat := twelve(1, 0.002)
	tests := []struct {
		name          string
		base, head    map[string][]float64
		control       map[string][]float64
		headAllocs    int
		controlAllocs int
		want          string
		reason        string
	}{
		{
			name:    "nothing changed",
			base:    map[string][]float64{"A": flat, "B": flat},
			head:    map[string][]float64{"A": flat, "B": flat},
			control: map[string][]float64{"A": flat, "B": flat},
			want:    verdictPass,
		},
		{
			name:    "one benchmark much slower",
			base:    map[string][]float64{"A": flat, "B": flat, "C": flat},
			head:    map[string][]float64{"A": twelve(1.10, 0.002), "B": flat, "C": flat},
			control: map[string][]float64{"A": flat, "B": flat, "C": flat},
			want:    verdictFail,
			reason:  "A is +10.0%",
		},
		{
			name:    "every benchmark a little slower",
			base:    map[string][]float64{"A": flat, "B": flat, "C": flat},
			head:    map[string][]float64{"A": twelve(1.03, 0.002), "B": twelve(1.03, 0.002), "C": twelve(1.03, 0.002)},
			control: map[string][]float64{"A": flat, "B": flat, "C": flat},
			want:    verdictFail,
			reason:  "geomean is +3.00%",
		},
		{
			name:    "faster is not a regression",
			base:    map[string][]float64{"A": flat, "B": flat},
			head:    map[string][]float64{"A": twelve(0.8, 0.002), "B": twelve(0.8, 0.002)},
			control: map[string][]float64{"A": flat, "B": flat},
			want:    verdictPass,
		},
		{
			// The control moved 8% on its own, so a 7% move by head is within what this
			// sitting does with nothing changed.
			name:    "slowdown within the control's noise",
			base:    map[string][]float64{"A": flat, "B": flat},
			head:    map[string][]float64{"A": twelve(1.07, 0.002), "B": flat},
			control: map[string][]float64{"A": twelve(1.08, 0.002), "B": twelve(0.96, 0.002)},
			want:    verdictPass,
		},
		{
			name:    "control past the ceiling",
			base:    map[string][]float64{"A": flat, "B": flat},
			head:    map[string][]float64{"A": twelve(1.30, 0.002), "B": flat},
			control: map[string][]float64{"A": twelve(1.20, 0.002), "B": flat},
			want:    verdictInconclusive,
			reason:  "past the 5% ceiling",
		},
		{
			name:       "one more allocation",
			base:       map[string][]float64{"A": flat},
			head:       map[string][]float64{"A": flat},
			control:    map[string][]float64{"A": flat},
			headAllocs: 4,
			want:       verdictFail,
			reason:     "A allocates 4 allocs/op, was 3",
		},
		{
			// Base and control disagree, so the metric is not deterministic for this
			// benchmark and head's count proves nothing.
			name:          "allocations that vary are not judged",
			base:          map[string][]float64{"A": flat},
			head:          map[string][]float64{"A": flat},
			control:       map[string][]float64{"A": flat},
			headAllocs:    4,
			controlAllocs: 5,
			want:          verdictPass,
		},
		{
			name:       "an allocation regression is judged on a noisy run too",
			base:       map[string][]float64{"A": flat},
			head:       map[string][]float64{"A": flat},
			control:    map[string][]float64{"A": twelve(1.5, 0.002)},
			headAllocs: 4,
			want:       verdictFail,
		},
		{
			// Too few samples for a significant result: with four a side, Mann-Whitney
			// cannot get below p=0.029, so a big move on one benchmark of eight - small
			// enough in the geomean - is not called.
			name:    "four samples cannot reach alpha",
			base:    eight(flat[:4], nil),
			head:    eight(flat[:4], twelve(1.10, 0.002)[:4]),
			control: eight(flat[:4], nil),
			want:    verdictPass,
		},
		{
			name:    "twelve samples can",
			base:    eight(flat, nil),
			head:    eight(flat, twelve(1.10, 0.002)),
			control: eight(flat, nil),
			want:    verdictFail,
			reason:  "A is +10.0%",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headAllocs, controlAllocs := 3, 3
			if tt.headAllocs != 0 {
				headAllocs = tt.headAllocs
			}
			if tt.controlAllocs != 0 {
				controlAllocs = tt.controlAllocs
			}
			r := build(bench(t, tt.base, 3), bench(t, tt.head, headAllocs), bench(t, tt.control, controlAllocs))
			judge(&r, defaultParams)
			if r.Verdict != tt.want {
				t.Fatalf("verdict %s, want %s; reasons: %q", r.Verdict, tt.want, r.Reasons)
			}
			if tt.reason != "" && !strings.Contains(strings.Join(r.Reasons, "\n"), tt.reason) {
				t.Errorf("reasons %q do not mention %q", r.Reasons, tt.reason)
			}
			if tt.want == verdictPass && len(r.Reasons) > 0 {
				t.Errorf("passed with reasons %q", r.Reasons)
			}
		})
	}
}

func TestBuildPairsByName(t *testing.T) {
	flat := twelve(1, 0)
	r := build(
		bench(t, map[string][]float64{"Old": flat, "Both": flat}, 0),
		bench(t, map[string][]float64{"New": flat, "Both": flat}, 0),
		bench(t, map[string][]float64{"Old": flat, "Both": flat}, 0))
	if len(r.Rows) != 1 || r.Rows[0].Name != "Both" {
		t.Errorf("rows %+v, want only Both", r.Rows)
	}
	if strings.Join(r.OnlyBase, ",") != "Old" || strings.Join(r.OnlyHead, ",") != "New" {
		t.Errorf("only in base %q, only in head %q", r.OnlyBase, r.OnlyHead)
	}
}

func TestReadKeepsGoTestUnits(t *testing.T) {
	s := bench(t, map[string][]float64{"A/sub": {1, 2}}, 7)
	units := s.values["A/sub"]
	if got := units["ns/op"]; len(got) != 2 || got[0] != 1000 || got[1] != 2000 {
		t.Errorf("ns/op %v, want [1000 2000]", got)
	}
	if got := units["allocs/op"]; len(got) != 2 || got[0] != 7 {
		t.Errorf("allocs/op %v, want [7 7]", got)
	}
	if s.config["cpu"] != "Test CPU" || s.config["goversion"] != "go1.26.0" {
		t.Errorf("config %v", s.config)
	}
}

func TestReadRejectsMalformedLines(t *testing.T) {
	_, err := read(strings.NewReader("BenchmarkA 1000 12 ns/op 4\n"), "bad")
	if err == nil {
		t.Fatal("no error for a line with a value but no unit")
	}
}

func TestCenter(t *testing.T) {
	tests := []struct {
		name      string
		values    []float64
		variant   []string
		c, spread float64
	}{
		// Two layout modes, as IterRead's walk shows on Zen 3. The median of the pool
		// would be 15.5, between the modes; the variants average to 15.
		{"bimodal", []float64{10, 20, 10, 20, 11, 21}, []string{"1", "2", "1", "2", "1", "2"}, 15, 1},
		{"one variant", []float64{3, 1, 2}, []string{"1", "1", "1"}, 2, 0},
		{"no variant recorded", []float64{3, 1, 2, 10}, nil, 2.5, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, spread := center(tt.values, tt.variant)
			if c != tt.c || spread != tt.spread {
				t.Errorf("center %v, spread %v; want %v, %v", c, spread, tt.c, tt.spread)
			}
		})
	}
}

func TestReadRecordsVariants(t *testing.T) {
	in := "variant: 1\nBenchmarkA 10 5 ns/op\nvariant: 2\nBenchmarkA 10 7 ns/op\nBenchmarkA 10 9 ns/op\n"
	s, err := read(strings.NewReader(in), "test")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.variant["A"], ","); got != "1,2,2" {
		t.Errorf("variants %s, want 1,2,2", got)
	}
}
