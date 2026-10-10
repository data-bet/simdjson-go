#!/usr/bin/env bash
# Copyright (c) 2026 DATA.BET
# Licensed under the Apache License, Version 2.0; see LICENSE at the repository root.
#
# Measure one revision of the parser against another on this machine, and judge the
# result with perfgate.
#
#   ab.sh -b BASE -h HEAD (-g GROUP | -p PATTERN) [-v VARIANTS] [-r ROUNDS]
#         [-t BENCHTIME] [-o OUTDIR] [-l LABEL] [-c CPU[,CPU...]] [-n]
#
# GROUP names one of the benchmark sets CI runs, each in a job of its own:
#
#   corpus  BenchmarkParseCorpus: the whole parser over every document, both copy modes
#   stage1  BenchmarkStage1 and BenchmarkStage1AVX2: stage 1 alone, on both pipelines,
#           since a runner with AVX-512 would otherwise never run the AVX2 one
#   read    reading a parsed document, ParseND with reuse, and the small payloads
#
# Three sets of test binaries are built, VARIANTS of each: HEAD, BASE, and BASE again
# as a control. Every binary of every set has the library's code at a different
# address. Where the hot loops land relative to cache lines and decoder windows can
# move a benchmark on its own, and a change that moves the code - any change - draws a
# new ticket in that lottery. Spreading each set over several layouts averages it out
# instead of sampling it once. How much it matters depends on the CPU, so it is not
# assumed: the control differs from BASE only in layout, and shows it.
#
# The layout is moved by a padding package that a test file imports: a function of NOPs
# and an initialised array, both reachable from its init. The linker places a package's
# dependencies before it, so the padding lands in front of the library and shifts its
# code and its data tables alike - the tables matter as much as the loops here, since
# the float parser's 11 KB power-of-ten table has been seen to run three times slower
# from conflict misses at one address. A padding variable in a test file does nothing:
# the linker drops it as unreferenced, and both binaries come out the same.
#
# The runs interleave - each round runs every binary once, in an order that rotates -
# so drift on the machine during the sitting lands on all three sets alike.
# GOMAXPROCS is 1: above 256 KB the two stages of the parser run concurrently, and how
# that race falls scatters far wider than any change worth catching.
#
# -c pins the runs to a CPU; given several, one worker runs on each, in parallel. The
# unit of work is one variant of one round - its base, control and head binaries back
# to back on one CPU - so whatever that CPU brings, its clock, its neighbours, lands on
# all three sets alike, and the units are dealt out to the workers in turn. Give
# physical cores, not SMT siblings, and see benchmarks/README.md for how many.
#
# -n skips perfgate's verdict from the exit status (report only), for calibration runs.
set -euo pipefail

base= head= pattern= label=
variants=4 rounds=3 benchtime=200ms out= cpu= report_only=
while getopts "b:h:g:p:v:r:t:o:l:c:n" opt; do
	case $opt in
	b) base=$OPTARG ;;
	g)
		case $OPTARG in
		corpus) pattern='^BenchmarkParseCorpus$' ;;
		stage1) pattern='^BenchmarkStage1(AVX2)?$' ;;
		read) pattern='^(BenchmarkIterRead|BenchmarkParseNDReuse|BenchmarkParse(Small|Medium|Large))$' ;;
		*)
			echo "ab.sh: unknown group $OPTARG" >&2
			exit 2
			;;
		esac
		label=${label:-$OPTARG}
		;;
	h) head=$OPTARG ;;
	p) pattern=$OPTARG ;;
	v) variants=$OPTARG ;;
	r) rounds=$OPTARG ;;
	t) benchtime=$OPTARG ;;
	o) out=$OPTARG ;;
	l) label=$OPTARG ;;
	c) cpu=$OPTARG ;;
	n) report_only=1 ;;
	*) exit 2 ;;
	esac
done
if [ -z "$base" ] || [ -z "$head" ] || [ -z "$pattern" ]; then
	sed -n '8,9p' "$0" >&2
	exit 2
fi
# The padding step is 32 bytes past a multiple of 64, so consecutive variants alternate
# between the two 32-byte halves of a cache line. With an even number of variants every
# set gets as many of each, however far a change moves the code, and perfgate averages
# over the variants - see center in compare.go for the 32% that makes this matter.
if [ $((variants % 2)) -ne 0 ]; then
	echo "ab.sh: -v must be even, so that every set has as many layouts of each alignment" >&2
	exit 2
fi
if [ "$(go env GOARCH)" != amd64 ]; then
	echo "ab.sh: the layout padding is amd64 assembly; run on amd64" >&2
	exit 2
fi

here=$(cd "$(dirname "$0")" && pwd)
repo=$(git -C "$here" rev-parse --show-toplevel)
work=$(mktemp -d)
out=${out:-$work/out}
label=${label:-$pattern}
mkdir -p "$out"
out=$(cd "$out" && pwd)

cleanup() {
	git -C "$repo" worktree remove --force "$work/base" >/dev/null 2>&1 || true
	git -C "$repo" worktree remove --force "$work/head" >/dev/null 2>&1 || true
	rm -rf "$work/base" "$work/head" "$work/bin" "$work/pad" "$work"/w*.txt
	# Gone unless the output was left inside it.
	rmdir "$work" 2>/dev/null || true
}
trap cleanup EXIT

# pad writes, under directory $3, the padding package for a tree whose module is $1 so
# that it is $2 bytes long, the test file that pulls it in, and an overlay that adds
# both to the tree at $4 for go test -c - so that several variants can be built at
# once from one tree. The package path follows the tree's own module path, which
# changes across history.
pad() {
	local module=$1 bytes=$2 dir=$3 tree=$4
	mkdir -p "$dir"
	cat >"$dir/pad.go" <<EOF
// Package zzlayoutpad moves the code and data after it; see benchmarks/perfgate/ab.sh.
package zzlayoutpad

func pad()

var padData = [$bytes]byte{1}

var Sink byte

func init() {
	pad()
	Sink = padData[len(padData)-1]
}
EOF
	{
		echo '#include "textflag.h"'
		echo
		echo 'TEXT ·pad(SB), NOSPLIT, $0-0'
		for ((k = 0; k < bytes / 8; k++)); do
			echo '	QUAD $0x9090909090909090'
		done
		echo '	RET'
	} >"$dir/pad_amd64.s"
	cat >"$dir/zz_layout_pad_test.go" <<EOF
package simdjson

import _ "$module/internal/zzlayoutpad"
EOF
	cat >"$dir/overlay.json" <<EOF
{"Replace": {
	"$tree/internal/zzlayoutpad/pad.go": "$dir/pad.go",
	"$tree/internal/zzlayoutpad/pad_amd64.s": "$dir/pad_amd64.s",
	"$tree/zz_layout_pad_test.go": "$dir/zz_layout_pad_test.go"
}}
EOF
}

# Steps of 1056 bytes: a multiple of the 32-byte function alignment, and not of 64, so
# the variants alternate between the two halves of a cache line as well as moving
# through the page - which is where the data tables' cache sets come from.
step=1056
mkdir -p "$work/bin"
pids=()
for side in base head; do
	rev=$base
	[ $side = head ] && rev=$head
	git -C "$repo" worktree add --detach "$work/$side" "$rev" >/dev/null
	commit=$(git -C "$work/$side" rev-parse HEAD)
	module=$(cd "$work/$side" && go list -m)
	# The overlay supplies the padding package's files, but the assembler still runs
	# in the package's directory, so that has to exist.
	mkdir -p "$work/$side/internal/zzlayoutpad"
	groups=$side
	[ $side = base ] && groups="base control"
	for group in $groups; do
		for i in $(seq 1 "$variants"); do
			# Every binary of all three groups gets its own offset.
			case $group in
			base) slot=$i ;;
			control) slot=$((variants + i)) ;;
			head) slot=$((2 * variants + i)) ;;
			esac
			pad "$module" $((slot * step)) "$work/pad/$group.$i" "$work/$side"
			(cd "$work/$side" && CGO_ENABLED=0 go test -c \
				-overlay "$work/pad/$group.$i/overlay.json" -o "$work/bin/$group.$i" .) &
			pids+=($!)
			# go test -c is parallel inside already; four at a time keeps the machine busy.
			if [ ${#pids[@]} -ge 4 ]; then
				wait "${pids[0]}"
				pids=("${pids[@]:1}")
			fi
		done
		# perfgate reads these as file configuration.
		{
			echo "commit: $commit"
			echo "goversion: $(go env GOVERSION)"
		} >"$out/$group.txt"
	done
done
for pid in "${pids[@]}"; do
	wait "$pid"
done

cpus=()
if [ -n "$cpu" ]; then
	IFS=, read -r -a cpus <<<"$cpu"
fi
workers=${#cpus[@]}
if [ "$workers" -eq 0 ]; then
	workers=1
fi

# worker runs every unit dealt to worker $1, pinned to CPU $2 if there is one, into
# files of its own.
worker() {
	local w=$1 cpu=$2 n=0 round i k group dir
	local pin=()
	if [ -n "$cpu" ]; then
		pin=(taskset -c "$cpu")
	fi
	local groups=(base control head)
	for round in $(seq 1 "$rounds"); do
		for i in $(seq 1 "$variants"); do
			if [ $((n % workers)) -eq "$w" ]; then
				for k in 0 1 2; do
					group=${groups[$(((round + i + k) % 3))]}
					# Test binaries find testdata relative to the package directory.
					dir=$work/base
					[ "$group" = head ] && dir=$work/head
					echo "variant: $i" >>"$work/w$w.$group.txt"
					(cd "$dir" && "${pin[@]}" "$work/bin/$group.$i" -test.run '^$' \
						-test.bench "$pattern" -test.benchtime "$benchtime" -test.cpu 1 \
						>>"$work/w$w.$group.txt")
				done
			fi
			n=$((n + 1))
		done
	done
}

echo "measuring $label: $variants variants x $rounds rounds, benchtime $benchtime, $workers worker(s)"
pids=()
for ((w = 0; w < workers; w++)); do
	worker "$w" "${cpus[$w]:-}" &
	pids+=($!)
done
failed=0
for pid in "${pids[@]}"; do
	wait "$pid" || failed=1
done
if [ $failed -ne 0 ]; then
	echo "ab.sh: a benchmark run failed" >&2
	exit 2
fi
for group in base control head; do
	for ((w = 0; w < workers; w++)); do
		if [ -f "$work/w$w.$group.txt" ]; then
			cat "$work/w$w.$group.txt" >>"$out/$group.txt"
		fi
	done
done

status=0
(cd "$here/.." && go run ./perfgate compare -label "$label" \
	-base "$out/base.txt" -head "$out/head.txt" -control "$out/control.txt" \
	-json "$out/report.json" ${GITHUB_STEP_SUMMARY:+-md "$GITHUB_STEP_SUMMARY"}) || status=$?
echo "raw output: $out"
if [ -n "$report_only" ] && [ $status -eq 1 ]; then
	status=0
fi
exit $status
