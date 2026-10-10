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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"
	"strings"

	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"
)

// errRegressed is returned by runCompare when the verdict is a regression, so that
// main can tell it from a failure to run at all.
var errRegressed = errors.New("regressed")

// Params are the rules a comparison is judged by. The defaults were checked against
// perf-calibrate.yml on GitHub's ubuntu-24.04 runners (EPYC 7763, 9V45 and 9V74, Xeon
// 8370C and 8573C), October 2026: 48 null comparisons, eight per group in each
// direction, gave no false alarm and no inconclusive verdict, and all four undone
// optimisations were caught. The largest move with nothing changed was 1.9% on one
// corpus benchmark, 3.7% on stage 1 and 5.3% on reading - which the 5% floor meets only
// together with the per-benchmark control and the significance test - and 1.3% on a
// group's geomean. See calibrate.go for how to re-derive them.
type Params struct {
	// MinSingle and MinGeo are the smallest slowdowns that count, for one benchmark
	// and for the geometric mean of a group, however quiet the control was.
	MinSingle float64 `json:"min_single"`
	MinGeo    float64 `json:"min_geo"`

	// SingleNoise and GeoNoise scale the control's own deviation into a threshold:
	// a benchmark has to move further than its control did, times SingleNoise, and
	// the geomean further than the control's geomean, times GeoNoise. Per benchmark,
	// so that one benchmark which is noisy in this sitting is judged leniently without
	// blunting the others.
	SingleNoise float64 `json:"single_noise"`
	GeoNoise    float64 `json:"geo_noise"`

	// Alpha is the significance a single benchmark's slowdown needs, by Mann-Whitney U
	// between the head and base samples.
	Alpha float64 `json:"alpha"`

	// Ceiling is the largest geomean threshold a verdict is worth giving at. A sitting
	// whose control moved so far that the threshold would have to be higher cannot
	// see the regressions the gate is for, and is reported as inconclusive.
	Ceiling float64 `json:"ceiling"`
}

var defaultParams = Params{
	MinSingle:   0.05,
	MinGeo:      0.02,
	SingleNoise: 1.0,
	GeoNoise:    2.0,
	Alpha:       0.01,
	Ceiling:     0.05,
}

// Row is one benchmark's time per operation: medians of each group, and how head and
// control moved relative to base.
type Row struct {
	Name         string  `json:"name"`
	Base         float64 `json:"base_ns"`
	Head         float64 `json:"head_ns"`
	Control      float64 `json:"control_ns"`
	Layout       float64 `json:"layout"` // spread between base's layout variants
	N            [3]int  `json:"n"`      // samples: base, head, control
	Delta        float64 `json:"delta"`
	ControlDelta float64 `json:"control_delta"`
	Threshold    float64 `json:"threshold"`
	P            float64 `json:"p"`         // head against base
	ControlP     float64 `json:"control_p"` // control against base
	Regressed    bool    `json:"regressed,omitempty"`
	Improved     bool    `json:"improved,omitempty"`
}

// AllocRow is one allocation metric of one benchmark. Allocation counts do not depend
// on the machine, so these are judged exactly - but only where base and control agree
// sample for sample, which is what shows the metric is deterministic for this benchmark.
type AllocRow struct {
	Name          string  `json:"name"`
	Unit          string  `json:"unit"`
	Base          float64 `json:"base"`
	Head          float64 `json:"head"`
	Deterministic bool    `json:"deterministic"`
	Regressed     bool    `json:"regressed,omitempty"`
}

// Report is everything one comparison found. It is written as JSON so that calibrate
// can re-judge many of them under other parameters.
type Report struct {
	Label     string     `json:"label,omitempty"`
	CPU       string     `json:"cpu,omitempty"`
	GoVersion string     `json:"goversion,omitempty"`
	BaseRev   string     `json:"base_rev,omitempty"`
	HeadRev   string     `json:"head_rev,omitempty"`
	Params    Params     `json:"params"`
	Rows      []Row      `json:"rows"`
	Allocs    []AllocRow `json:"allocs"`
	OnlyBase  []string   `json:"only_base,omitempty"`
	OnlyHead  []string   `json:"only_head,omitempty"`

	Geomean        float64  `json:"geomean"`
	ControlGeomean float64  `json:"control_geomean"`
	Noise          float64  `json:"noise"` // largest |ControlDelta|, for the record
	GeoThreshold   float64  `json:"geo_threshold"`
	Verdict        string   `json:"verdict"` // pass, fail or inconclusive
	Reasons        []string `json:"reasons,omitempty"`
}

const (
	verdictPass         = "pass"
	verdictFail         = "fail"
	verdictInconclusive = "inconclusive"
)

func runCompare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	basePath := fs.String("base", "", "benchmark output of the base revision")
	headPath := fs.String("head", "", "benchmark output of the change")
	controlPath := fs.String("control", "", "benchmark output of base, built with another layout")
	jsonPath := fs.String("json", "", "write the report as JSON here")
	mdPath := fs.String("md", "", "append the report as Markdown here (e.g. $GITHUB_STEP_SUMMARY)")
	label := fs.String("label", "", "name of this comparison, carried into the report")
	p := defaultParams
	fs.Float64Var(&p.MinSingle, "min-single", p.MinSingle, "smallest single-benchmark slowdown that counts")
	fs.Float64Var(&p.MinGeo, "min-geo", p.MinGeo, "smallest geomean slowdown that counts")
	fs.Float64Var(&p.SingleNoise, "single-noise", p.SingleNoise, "single threshold as a multiple of the control's largest deviation")
	fs.Float64Var(&p.GeoNoise, "geo-noise", p.GeoNoise, "geomean threshold as a multiple of the control's geomean deviation")
	fs.Float64Var(&p.Alpha, "alpha", p.Alpha, "significance a single-benchmark slowdown needs")
	fs.Float64Var(&p.Ceiling, "ceiling", p.Ceiling, "control deviation past which the run is inconclusive")
	fs.Parse(args)
	if *basePath == "" || *headPath == "" || *controlPath == "" {
		return errors.New("compare needs -base, -head and -control")
	}

	base, err := readFile(*basePath)
	if err != nil {
		return err
	}
	head, err := readFile(*headPath)
	if err != nil {
		return err
	}
	control, err := readFile(*controlPath)
	if err != nil {
		return err
	}

	r := build(base, head, control)
	r.Label = *label
	r.CPU = base.config["cpu"]
	r.GoVersion = base.config["goversion"]
	r.BaseRev = base.config["commit"]
	r.HeadRev = head.config["commit"]
	if len(r.Rows) == 0 {
		return errors.New("no benchmark appears in all three files")
	}
	judge(&r, p)

	if *jsonPath != "" {
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*jsonPath, b, 0o644); err != nil {
			return err
		}
	}
	writeText(os.Stdout, &r)
	if *mdPath != "" {
		f, err := os.OpenFile(*mdPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		writeMarkdown(f, &r)
		if err := f.Close(); err != nil {
			return err
		}
	}
	if r.Verdict == verdictFail {
		return errRegressed
	}
	return nil
}

// samples holds every value read for one group, by benchmark name and then unit, and
// for each benchmark which layout variant each of its results came from.
type samples struct {
	values  map[string]map[string][]float64
	variant map[string][]string
	config  map[string]string
}

func readFile(path string) (*samples, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return read(f, path)
}

func read(r io.Reader, name string) (*samples, error) {
	s := &samples{
		values:  map[string]map[string][]float64{},
		variant: map[string][]string{},
		config:  map[string]string{},
	}
	br := benchfmt.NewReader(r, name)
	for br.Scan() {
		switch rec := br.Result().(type) {
		case *benchfmt.SyntaxError:
			return nil, rec
		case *benchfmt.Result:
			for _, c := range rec.Config {
				if c.File {
					s.config[c.Key] = string(c.Value)
				}
			}
			// -test.cpu 1 drops the -N suffix; any other setting would split one
			// benchmark into several names here, which is what we want.
			full := string(rec.Name.Full())
			units := s.values[full]
			if units == nil {
				units = map[string][]float64{}
				s.values[full] = units
			}
			s.variant[full] = append(s.variant[full], rec.GetConfig("variant"))
			// The reader tidies ns/op into sec/op; keep the units as go test wrote them.
			for _, v := range rec.Values {
				unit, value := v.Unit, v.Value
				if v.OrigUnit != "" {
					unit, value = v.OrigUnit, v.OrigValue
				}
				units[unit] = append(units[unit], value)
			}
		}
	}
	return s, br.Err()
}

// build pairs up the three groups. A benchmark missing from any of them cannot be
// judged; one the change added or removed is listed, not compared.
func build(base, head, control *samples) Report {
	var r Report
	for name := range base.values {
		if _, ok := head.values[name]; !ok {
			r.OnlyBase = append(r.OnlyBase, name)
		}
	}
	for name := range head.values {
		if _, ok := base.values[name]; !ok {
			r.OnlyHead = append(r.OnlyHead, name)
		}
	}
	sort.Strings(r.OnlyBase)
	sort.Strings(r.OnlyHead)

	names := make([]string, 0, len(base.values))
	for name := range base.values {
		_, inHead := head.values[name]
		_, inControl := control.values[name]
		if inHead && inControl {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		b, h, c := base.values[name], head.values[name], control.values[name]
		if bt, ok := b["ns/op"]; ok && len(h["ns/op"]) > 0 && len(c["ns/op"]) > 0 {
			ht, ct := h["ns/op"], c["ns/op"]
			bc, spread := center(bt, base.variant[name])
			hc, _ := center(ht, head.variant[name])
			cc, _ := center(ct, control.variant[name])
			row := Row{
				Name:    name,
				Base:    bc,
				Head:    hc,
				Control: cc,
				Layout:  spread,
				N:       [3]int{len(bt), len(ht), len(ct)},
			}
			row.Delta = row.Head/row.Base - 1
			row.ControlDelta = row.Control/row.Base - 1
			row.P = mannWhitney(bt, ht)
			row.ControlP = mannWhitney(bt, ct)
			r.Rows = append(r.Rows, row)
		}
		for _, unit := range []string{"allocs/op", "B/op"} {
			bv, hv, cv := b[unit], h[unit], c[unit]
			if len(bv) == 0 || len(hv) == 0 || len(cv) == 0 {
				continue
			}
			r.Allocs = append(r.Allocs, AllocRow{
				Name:          name,
				Unit:          unit,
				Base:          median(bv),
				Head:          median(hv),
				Deterministic: allEqual(bv, cv),
			})
		}
	}
	return r
}

// judge sets the thresholds, flags and verdict of r under p. It reads nothing but the
// medians, deltas and p-values already in r, so calibrate can call it again on a report
// read back from JSON.
func judge(r *Report, p Params) {
	r.Params = p
	r.Reasons = nil

	var logDelta, logControl float64
	r.Noise = 0
	for _, row := range r.Rows {
		logDelta += math.Log1p(row.Delta)
		logControl += math.Log1p(row.ControlDelta)
		r.Noise = max(r.Noise, math.Abs(row.ControlDelta))
	}
	n := float64(len(r.Rows))
	r.Geomean = math.Expm1(logDelta / n)
	r.ControlGeomean = math.Expm1(logControl / n)
	r.GeoThreshold = max(p.MinGeo, p.GeoNoise*math.Abs(r.ControlGeomean))

	regressed := 0
	for i := range r.Rows {
		row := &r.Rows[i]
		row.Threshold = max(p.MinSingle, p.SingleNoise*math.Abs(row.ControlDelta))
		significant := row.P < p.Alpha
		row.Regressed = significant && row.Delta > row.Threshold
		row.Improved = significant && row.Delta < -row.Threshold
		if row.Regressed {
			regressed++
			r.Reasons = append(r.Reasons, fmt.Sprintf("%s is %+.1f%% (threshold %.1f%%, p=%.3g)",
				row.Name, row.Delta*100, row.Threshold*100, row.P))
		}
	}
	if r.Geomean > r.GeoThreshold {
		regressed++
		r.Reasons = append(r.Reasons, fmt.Sprintf("geomean is %+.2f%% (threshold %.2f%%)",
			r.Geomean*100, r.GeoThreshold*100))
	}

	// Allocations are judged even on a noisy run: they do not depend on timing.
	allocRegressed := 0
	for i := range r.Allocs {
		a := &r.Allocs[i]
		a.Regressed = a.Deterministic && a.Head > a.Base
		if a.Regressed {
			allocRegressed++
			r.Reasons = append(r.Reasons, fmt.Sprintf("%s allocates %g %s, was %g",
				a.Name, a.Head, a.Unit, a.Base))
		}
	}

	switch {
	case allocRegressed > 0:
		r.Verdict = verdictFail
	case r.GeoThreshold > p.Ceiling:
		// The control moved so far that only a regression larger than any worth
		// catching would show; whatever head did, this sitting cannot tell.
		r.Verdict = verdictInconclusive
		r.Reasons = append([]string{fmt.Sprintf(
			"control geomean moved %+.2f%% with nothing changed, so the threshold would be %.1f%%, past the %.0f%% ceiling",
			r.ControlGeomean*100, r.GeoThreshold*100, p.Ceiling*100)}, r.Reasons...)
	case regressed > 0:
		r.Verdict = verdictFail
	default:
		r.Verdict = verdictPass
	}
}

func mannWhitney(a, b []float64) float64 {
	return benchmath.AssumeNothing.Compare(
		benchmath.NewSample(a, &benchmath.DefaultThresholds),
		benchmath.NewSample(b, &benchmath.DefaultThresholds)).P
}

// center is the time a group stands for: the median of each layout variant's samples,
// averaged over the variants. Not the median of all of them: a benchmark can be bimodal
// in its layout - IterRead's walk loop runs 32% slower on Zen 3 when it starts 32 bytes
// into a cache line rather than on one - and the median of a pool that is half one mode
// and half the other lands in the gap, wherever a single sample falls. ab.sh gives each
// group as many variants of each alignment, so averaging the variants measures every
// group across the same mix of layouts. spread is how far the variants' medians lie
// apart, max over min, minus one.
func center(v []float64, variant []string) (c, spread float64) {
	if len(variant) != len(v) {
		return median(v), 0
	}
	byVariant := map[string][]float64{}
	var order []string
	for i, x := range v {
		if _, ok := byVariant[variant[i]]; !ok {
			order = append(order, variant[i])
		}
		byVariant[variant[i]] = append(byVariant[variant[i]], x)
	}
	lo, hi, sum := math.Inf(1), math.Inf(-1), 0.0
	for _, k := range order {
		m := median(byVariant[k])
		sum += m
		lo, hi = min(lo, m), max(hi, m)
	}
	return sum / float64(len(order)), hi/lo - 1
}

func median(v []float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// allEqual reports whether every value in a and b is the same single value.
func allEqual(a, b []float64) bool {
	for _, v := range a {
		if v != a[0] {
			return false
		}
	}
	for _, v := range b {
		if v != a[0] {
			return false
		}
	}
	return true
}

func writeText(w io.Writer, r *Report) {
	fmt.Fprintf(w, "%s: %s\n", strings.ToUpper(r.Verdict), r.Label)
	fmt.Fprintf(w, "cpu: %s  go: %s\n", r.CPU, r.GoVersion)
	fmt.Fprintf(w, "geomean %+.2f%% (threshold %.2f%%), control geomean %+.2f%%, control worst %.2f%%\n",
		r.Geomean*100, r.GeoThreshold*100, r.ControlGeomean*100, r.Noise*100)
	for _, reason := range r.Reasons {
		fmt.Fprintln(w, "  -", reason)
	}
	width := 0
	for _, row := range r.Rows {
		width = max(width, len(row.Name))
	}
	for _, row := range r.Rows {
		fmt.Fprintf(w, "%-*s  %12.0f  %12.0f  %+7.2f%%  ctrl %+6.2f%%  thr %5.2f%%  layout %5.1f%%  p=%.3g%s\n",
			width, row.Name, row.Base, row.Head, row.Delta*100, row.ControlDelta*100,
			row.Threshold*100, row.Layout*100, row.P, mark(row))
	}
}

func mark(row Row) string {
	switch {
	case row.Regressed:
		return "  REGRESSED"
	case row.Improved:
		return "  improved"
	}
	return ""
}

func writeMarkdown(w io.Writer, r *Report) {
	icon := map[string]string{verdictPass: "✅", verdictFail: "❌", verdictInconclusive: "⚠️"}[r.Verdict]
	fmt.Fprintf(w, "### %s %s: %s\n\n", icon, r.Label, r.Verdict)
	fmt.Fprintf(w, "`%s` against `%s` on %s, %s.\n\n", short(r.HeadRev), short(r.BaseRev), r.CPU, r.GoVersion)
	fmt.Fprintf(w, "| | change | threshold |\n|---|---|---|\n")
	fmt.Fprintf(w, "| geomean | %+.2f%% | %.2f%% |\n", r.Geomean*100, r.GeoThreshold*100)
	fmt.Fprintf(w, "| control geomean (nothing changed) | %+.2f%% | |\n", r.ControlGeomean*100)
	fmt.Fprintf(w, "| control, largest single move | %.2f%% | |\n\n", r.Noise*100)
	for _, reason := range r.Reasons {
		fmt.Fprintf(w, "- %s\n", reason)
	}
	if len(r.Reasons) > 0 {
		fmt.Fprintln(w)
	}
	if len(r.OnlyBase)+len(r.OnlyHead) > 0 {
		fmt.Fprintf(w, "Not compared - only in base: %d, only in head: %d.\n\n", len(r.OnlyBase), len(r.OnlyHead))
	}

	rows := slices.Clone(r.Rows)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Delta > rows[j].Delta })
	fmt.Fprintf(w, "<details><summary>All %d benchmarks, slowest change first</summary>\n\n", len(rows))
	fmt.Fprintf(w, "| benchmark | base | head | change | control | threshold | layout spread | p | |\n|---|---:|---:|---:|---:|---:|---:|---:|---|\n")
	for _, row := range rows {
		fmt.Fprintf(w, "| %s | %s | %s | %+.2f%% | %+.2f%% | %.2f%% | %.1f%% | %.3g | %s |\n",
			row.Name, duration(row.Base), duration(row.Head), row.Delta*100, row.ControlDelta*100,
			row.Threshold*100, row.Layout*100, row.P, strings.TrimSpace(mark(row)))
	}
	fmt.Fprintf(w, "\n</details>\n\n")

	var nondet []string
	for _, a := range r.Allocs {
		if !a.Deterministic {
			nondet = append(nondet, a.Name+" "+a.Unit)
		}
	}
	if len(nondet) > 0 {
		fmt.Fprintf(w, "Allocation metrics not judged, since base and control disagreed: %s.\n\n",
			strings.Join(nondet, ", "))
	}
}

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

func duration(ns float64) string {
	switch {
	case ns >= 1e6:
		return fmt.Sprintf("%.2f ms", ns/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.1f µs", ns/1e3)
	}
	return fmt.Sprintf("%.0f ns", ns)
}
