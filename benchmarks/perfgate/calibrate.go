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
	"strings"
)

// runCalibrate reads reports from perf-calibrate.yml and says how the gate behaves on
// them. Reports whose label starts with "aa" compared a revision with itself, so any
// regression they show is a false alarm; the others are positive controls, a known
// speed-up measured backwards, and should fail.
//
// It prints how far the null comparisons strayed - per benchmark and per run - and then
// the false alarm rate and the controls' verdicts under the default parameters and under
// thresholds derived from what was observed. Both head-against-base and
// control-against-base of an A/A run are null comparisons, so each run counts twice.
func runCalibrate(args []string) error {
	fs := flag.NewFlagSet("calibrate", flag.ExitOnError)
	mdPath := fs.String("md", "", "append the summary as Markdown here")
	fs.Parse(args)
	if fs.NArg() == 0 {
		return errors.New("calibrate needs report files")
	}
	var reports []Report
	for _, path := range fs.Args() {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var r Report
		if err := json.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if r.Label == "" {
			r.Label = path
		}
		reports = append(reports, r)
	}

	var out strings.Builder
	calibrate(&out, reports)
	fmt.Print(out.String())
	if *mdPath != "" {
		f, err := os.OpenFile(*mdPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		fmt.Fprintf(f, "```\n%s```\n", out.String())
		return f.Close()
	}
	return nil
}

func calibrate(w io.Writer, reports []Report) {
	var null, controls []Report
	for _, r := range reports {
		if strings.HasPrefix(r.Label, "aa") {
			null = append(null, r, swapControl(r))
		} else {
			controls = append(controls, r)
		}
	}

	if len(null) > 0 {
		var single []float64
		for _, r := range null {
			for _, row := range r.Rows {
				single = append(single, math.Abs(row.Delta))
			}
		}
		worst, geo := worstAbs(null), geoAbs(null)
		fmt.Fprintf(w, "null comparisons: %d (from %d A/A runs)\n", len(null), len(null)/2)
		fmt.Fprintf(w, "  |delta| of one benchmark   %s\n", quantiles(single))
		fmt.Fprintf(w, "  worst benchmark of a run   %s\n", quantiles(worst))
		fmt.Fprintf(w, "  |geomean| of a run         %s\n", quantiles(geo))
		fmt.Fprintf(w, "  cpus: %s\n\n", cpus(null))
	}

	fmt.Fprintf(w, "default parameters: %+v\n", defaultParams)
	verdicts(w, null, controls, defaultParams)

	if len(null) > 0 {
		// The floors that would have kept every observed null comparison quiet on
		// their own, with a margin, rounded up to half a percent.
		p := defaultParams
		p.MinSingle = roundUp(1.2 * maxOf(worstAbs(null)))
		p.MinGeo = roundUp(1.5 * maxOf(geoAbs(null)))
		fmt.Fprintf(w, "\nderived floors: %+v\n", p)
		verdicts(w, null, controls, p)
	}
}

func worstAbs(reports []Report) []float64 {
	var out []float64
	for _, r := range reports {
		w := 0.0
		for _, row := range r.Rows {
			w = max(w, math.Abs(row.Delta))
		}
		out = append(out, w)
	}
	return out
}

func geoAbs(reports []Report) []float64 {
	var out []float64
	for _, r := range reports {
		logSum := 0.0
		for _, row := range r.Rows {
			logSum += math.Log1p(row.Delta)
		}
		out = append(out, math.Abs(math.Expm1(logSum/float64(len(r.Rows)))))
	}
	return out
}

func maxOf(v []float64) float64 {
	m := 0.0
	for _, x := range v {
		m = max(m, x)
	}
	return m
}

func roundUp(f float64) float64 {
	return math.Ceil(f*200) / 200
}

// swapControl turns an A/A report's control-against-base into a report of its own,
// so that it is judged like head-against-base, with head as its control.
func swapControl(r Report) Report {
	s := r
	s.Label = r.Label + "/control"
	s.Rows = slices.Clone(r.Rows)
	for i := range s.Rows {
		row := &s.Rows[i]
		row.Delta, row.ControlDelta = row.ControlDelta, row.Delta
		row.P, row.ControlP = row.ControlP, row.P
	}
	s.Allocs = nil
	return s
}

func verdicts(w io.Writer, null, controls []Report, p Params) {
	counts := map[string]int{}
	for _, r := range null {
		judge(&r, p)
		counts[r.Verdict]++
		if r.Verdict == verdictFail {
			fmt.Fprintf(w, "  false alarm: %s: %s\n", r.Label, strings.Join(r.Reasons, "; "))
		}
	}
	if len(null) > 0 {
		fmt.Fprintf(w, "  null comparisons: %d pass, %d fail, %d inconclusive\n",
			counts[verdictPass], counts[verdictFail], counts[verdictInconclusive])
	}
	for _, r := range controls {
		judge(&r, p)
		fmt.Fprintf(w, "  control %s: %s, geomean %+.2f%%, worst %+.2f%%\n",
			r.Label, r.Verdict, r.Geomean*100, worstDelta(r)*100)
	}
}

func worstDelta(r Report) float64 {
	w := math.Inf(-1)
	for _, row := range r.Rows {
		w = max(w, row.Delta)
	}
	return w
}

func quantiles(v []float64) string {
	if len(v) == 0 {
		return "-"
	}
	s := slices.Clone(v)
	slices.Sort(s)
	q := func(f float64) float64 { return s[int(f*float64(len(s)-1))] * 100 }
	return fmt.Sprintf("p50 %.2f%%  p90 %.2f%%  p99 %.2f%%  max %.2f%%  (n=%d)",
		q(0.5), q(0.9), q(0.99), s[len(s)-1]*100, len(s))
}

func cpus(reports []Report) string {
	seen := map[string]int{}
	for _, r := range reports {
		seen[r.CPU]++
	}
	var parts []string
	for cpu, n := range seen {
		parts = append(parts, fmt.Sprintf("%s x%d", cpu, n/2))
	}
	slices.Sort(parts)
	return strings.Join(parts, ", ")
}
