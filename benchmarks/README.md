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

## sonic

sonic has a native implementation only on the platforms and Go versions its own build
constraints name; elsewhere it forwards to `encoding/json`. `sonic_native_test.go`
carries that constraint verbatim, and the sonic rows skip rather than report
`encoding/json`'s numbers under sonic's name. A skipped sonic row means this build is
outside it — check the toolchain version before concluding anything about sonic.
