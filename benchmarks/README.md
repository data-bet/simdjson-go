# Comparison benchmarks

A separate module, so that the libraries compared here stay out of the root `go.mod`.
`go test ./...` from the repository root does not reach it — run it from this directory.

## What is here

| group | file | question it answers |
|---|---|---|
| `BenchmarkEncodingJson*`, `BenchmarkSonic*`, `BenchmarkSimdJson*`, `BenchmarkBugerJsonParserLarge` | `benchmarks_test.go` | the historical rows, kept for continuity with the numbers in the top-level README |
| `BenchmarkMaterialise` | `samework_test.go` | how long each library takes to turn a document into Go values |
| `BenchmarkExtract` | `samework_test.go` | how long each library takes to read three fields out of a document |

**The historical group is not a parser comparison.** `simdjson.Parse` builds a tape and
stops; `encoding/json` and sonic build a Go value out of every element. On `twitter` the
gap between the simdjson and sonic rows there is 4.9×, and on the same input
`BenchmarkMaterialise` — where both produce the same `interface{}` — it is 1.14×. Almost
all of the difference is what the two calls return, not how fast they read JSON. Compare
within the historical group, or use `BenchmarkMaterialise` to compare across libraries.

The header comment in `samework_test.go` lists every asymmetry between the libraries and
which of them were equalised. Read it before quoting a number from either fair group.

## Running

`benchstat` is pinned as a tool dependency of this module, so `go tool benchstat` runs
the version `go.mod` names and there is nothing to install first.

```sh
# Sequential rows: pin them. Two cores, not one - simdjson runs its two stages in
# separate goroutines above 256 KB, and a single-core pin measures a handicap.
taskset -c 2,3 go test -run '^$' -bench BenchmarkMaterialise \
    -benchtime 2s -count 10 > mat.txt

go tool benchstat -col /lib -row /file -filter '/mode:seq' mat.txt
```

The `mode=par` rows want every core, so run them unpinned and in their own invocation:

```sh
go test -run '^$' -bench BenchmarkMaterialise -benchtime 2s -count 10 > par.txt
go tool benchstat -col /lib -row /file -filter '/mode:par' par.txt
```

`BenchmarkExtract` has one axis, so it needs no filter:

```sh
taskset -c 2,3 go test -run '^$' -bench BenchmarkExtract -benchtime 2s -count 10 |
    go tool benchstat -col /lib /dev/stdin
```

Run `go test -run TestExtractAgree` first, and believe `BenchmarkExtract` only if it
passes: it checks that all the implementations return the same checksum. Without it the
fastest row is whichever one quietly found nothing.

## Two ways to get a wrong answer

**A short `-benchtime`.** The framework picks its iteration count from a first, short
measurement, and on allocation-heavy rows that measurement is noise. At `-benchtime
200ms` this suite reported 715 µs/op for sonic on `github_events`; at a fixed
`-benchtime 2000x` the same operation is 110 µs/op. Stay at or above `1s`, or pass a
fixed `Nx`.

**Not pinning.** `BenchmarkExtract` over six rounds spread ±73% for simdjson and ±59%
for `sonic_ast` unpinned, and ±3% and ±4% under `taskset` on one core — with the medians
about 1% apart. The spread was the scheduler moving rows between cores that do not hold
the same clock. Any conclusion drawn from an unpinned single round is noise.

## Reading the output

Read `B/op` and `allocs/op` next to `sec/op`, always. simdjson allocates almost nothing
per operation and the others allocate a whole tree, so a row that looks close on time
can differ by two orders of magnitude in garbage produced — which is what will show up
in a real service as GC time charged to something else. `runtime.GC()` runs before each
timer starts, which stops one row's garbage from landing on the next row's clock, but it
does not make the allocation free.

`GOGC=off` (with a `GOMEMLIMIT` that fits the run) answers a narrower question: how fast
each library fills memory with nobody reclaiming it. Useful next to the default numbers,
never instead of them.

## The performance gate in CI

`perfgate/` is a different job from the rest of this directory: not simdjson against
other libraries, but one revision of simdjson against another, to stop a change that
makes the parser slower from being merged unnoticed. `.github/workflows/perf.yml` runs it
on every pull request.

A hosted runner is a different machine every time - another CPU model, sometimes with
AVX-512 and sometimes without - so no number is ever stored and compared later.
`perfgate/ab.sh` builds both revisions on the same runner and measures them side by side
in one sitting:

- three sets of test binaries, base, head, and base again as a **control**, each built
  four times with the library's code and data tables at different addresses, half of
  them starting a cache line 32 bytes later than the other half. Each set's time is the
  average over its four layouts, not one layout's luck. This is not a fine point: on
  the Zen 3 runners `IterRead`'s walk loop takes 32% longer in one half of a cache line
  than in the other, and with three layouts a side the control came out 5% off base
  on every run, by having two of the fast kind where base had one;
- the runs interleaved over three rounds, so that drift during the sitting falls on all
  three sets alike;
- `GOMAXPROCS=1`, since above 256 KB the two stages race and how that falls scatters
  more widely than any change worth catching.

`perfgate compare` then judges medians. The control is base against itself, so it shows
how far this runner moves a benchmark with nothing changed, and every threshold is
raised to that: a benchmark fails if it is slower by more than 5% or by more than its
control moved, whichever is larger, and significantly so (Mann-Whitney, p < 0.01); the
group fails if its geomean is slower by more than 2% or by twice the control's geomean.
When the control is so noisy that the geomean threshold would pass 5%, the verdict is
*inconclusive*, not a failure. Allocations do not depend on the machine and are judged
exactly - one more `allocs/op` fails - wherever base and control agree sample for
sample.

Three groups run as separate jobs: `corpus` (`BenchmarkParseCorpus`), `stage1`
(`BenchmarkStage1` and `BenchmarkStage1AVX2`, so the AVX2 pipeline is measured even on an
AVX-512 runner) and `read` (`BenchmarkIterRead`, `BenchmarkParseNDReuse` and the
payloads). The report lands in the job summary, and the raw output is kept as an
artifact.

The same comparison runs locally, on amd64:

```sh
benchmarks/perfgate/ab.sh -g corpus -b master -h HEAD -c 2
```

Given several CPUs, `-c` runs one worker on each. The unit of work is one layout of one
round with its base, control and head runs back to back on one CPU, so a CPU's own
clock and neighbours fall on all three alike. Give physical cores and leave their SMT
siblings idle; leaving a core or two of each L3 complex to the rest of the system costs
little. Give a count that divides `-v` x `-r`, or the last wave runs part empty.

On a 16-core Ryzen AI MAX+ 395 (two L3 complexes, cores 0-7 and 8-15; the twelve
workers on 1-6 and 9-14), A/A runs of the `read` and `corpus` groups gave:

| workers | rounds | `read` takes | mean move with nothing changed, `read` | the same, `corpus` |
|---|---|---|---|---|
| 1 | 3 | 327 s | 0.66-0.90% | 0.15-0.23% |
| 4 | 3 | 84 s | 0.73-0.95% | 0.39-0.42% |
| 12 | 3 | 28 s | 0.93-1.11% | 0.52-0.64% |
| 12 | 6 | 54 s | 0.69-0.70% | 0.38-0.44% |

Twelve workers and twice the rounds are as quiet as one worker on `read` and twice as
noisy on `corpus`, in a sixth of the time; and a real change measured both ways agreed
to within a point on every benchmark that did not allocate (-20.2% and -19.9% geomean).
The exceptions were the `IterRead/*/objects` rows, which then allocated megabytes per
operation, shared memory bandwidth and the collector's work with eleven neighbours, and
moved by 3-6% in either direction, their controls with them; they no longer allocate.
A benchmark that allocates measures the collector as well, on one worker or twelve. For
a difference of a percent or less, measure on one worker.

```sh
benchmarks/perfgate/ab.sh -g read -b master -h HEAD -r 6 -c 1,2,3,4,5,6,9,10,11,12,13,14
```

Those thresholds were checked on the hosted runners before the gate went in: eight
runs per group of a revision against itself gave 48 null comparisons with no false
alarm and none inconclusive, the largest move with nothing changed being 1.9% on a
corpus benchmark, 3.7% on stage 1, 5.3% on reading and 1.3% on a geomean; and four
optimisations from the history, undone, were all caught - including one of about 2.5%
on the corpus.

`.github/workflows/perf-calibrate.yml` is how they were checked and how to check
them again: it runs a revision against itself many times, where every regression
reported is a false alarm, and undoes four optimisations from the history, which are
regressions of known size the gate should catch. Rerun it after changing `perfgate`, the
groups, or the runner image.
