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

// Command perfgate decides whether a change made the parser slower, from benchmark
// output that ab.sh collected on one machine in one sitting.
//
// It never compares against a number stored from an earlier run. A shared CI runner is
// a different machine every time - a different CPU model, sometimes with AVX-512 and
// sometimes without - so the only comparison it can make honestly is between two builds
// measured side by side. Three groups of samples come in:
//
//   - base: the revision the change is measured against
//   - head: the change
//   - control: base again, built with a different code layout
//
// head against base is the measurement; control against base is the same comparison
// with nothing changed, so it shows how far this machine, this layout lottery and this
// sitting move a benchmark on their own. Each benchmark's threshold is raised to what
// its control did, and the geomean's to what the control's geomean did; a sitting so
// noisy that the geomean threshold would pass the ceiling is reported as inconclusive
// rather than risking a false alarm.
//
//	perfgate compare -base base.txt -head head.txt -control control.txt [-json out.json] [-md out.md]
//	perfgate calibrate report.json...
//
// compare exits 1 when the change regressed, 0 otherwise - including when the run was
// too noisy to tell, which the report says. calibrate reads the JSON of several runs and
// prints how far null comparisons strayed, which is what the defaults are set from.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "compare":
		err = runCompare(os.Args[2:])
	case "calibrate":
		err = runCalibrate(os.Args[2:])
	default:
		usage()
	}
	if err == errRegressed {
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "perfgate:", err)
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: perfgate compare -base FILE -head FILE -control FILE [flags]")
	fmt.Fprintln(os.Stderr, "       perfgate calibrate REPORT.json...")
	os.Exit(2)
}
