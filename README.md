# simdjson-go

This is [DATA.BET](https://data.bet)'s fork of
[minio/simdjson-go](https://github.com/minio/simdjson-go). [NOTICE](NOTICE) says which
parts were inherited, which were changed here, and under which licence each stands.

The API is the same as upstream's, so moving over is a change of import path:

```bash
go get github.com/data-bet/simdjson-go@latest
```

```Go
import "github.com/data-bet/simdjson-go"
```

What differs from upstream `v0.4.5`:

- Parsing is about 35% faster than upstream with the default `WithCopyStrings(true)`
  (geometric mean over the test corpus), and `ParseND` with a reused `ParsedJson`
  allocates almost nothing.
- Reading a parsed document is about 30% faster (geometric mean over
  `BenchmarkIterRead`): a walk with `AdvanceInto` by 30-39%, one that also reads every
  value by 25-31%, one through `Object` and `NextElementBytes` by 12-22%, and one
  through `AdvanceIter` by 43-51%.
- Several inputs that upstream mishandled are fixed: nesting is capped at 1024 levels, as in
  simdjson, instead of being unbounded; a string scan bound that never fired now does; hex
  escapes with non-hex digits are rejected; and `ParseND` reuses the `ParsedJson` it is
  given. Some malformed documents that upstream accepted are therefore rejected now.
  On a corrupt tape `NextElementBytes` returns an error where it overflowed the stack.
- Platforms without the amd64 assembly get a portable implementation instead of an error,
  and `arm64` classifies with NEON - see [Requirements](#requirements).
- Optional UTF-8 validation with `WithValidateUTF8(true)`.
- Go 1.25 or newer is required.

## Introduction

This is a Golang port of [simdjson](https://github.com/lemire/simdjson),
a high performance JSON parser developed by Daniel Lemire and Geoff Langdale.
It makes extensive use of SIMD instructions to achieve parsing performance of gigabytes of JSON per second.

Parsing a document is about 10x faster than Golang's standard package `encoding/json`, and
allocates three to five orders of magnitude less - see
[Performance vs `encoding/json`](#performance-vs-encodingjson) for the measured figures, the
machine they were taken on and the command that reproduces them. The two calls do not do the
same amount of work, though: `Parse` builds a tape and stops, while `Unmarshal` builds a Go
value out of every element. `benchmarks/` holds a group where both sides are asked for the
same thing, and its README explains the asymmetries; on that footing simdjson-go is about
2.7x faster than `encoding/json` and roughly level with sonic.

How this compares to the C++ simdjson is not measured here.

[![Documentation](https://godoc.org/github.com/data-bet/simdjson-go?status.svg)](https://pkg.go.dev/github.com/data-bet/simdjson-go?tab=doc)

## Features

`simdjson-go` is a validating parser, meaning that it amongst others validates and checks numerical values, booleans etc.
 Therefore, these values are available as the appropriate `int` and `float64` representations after parsing.

Additionally `simdjson-go` has the following features:

- No 4 GB object limit
- Support for [ndjson](https://github.com/ndjson/ndjson-spec) (newline delimited json)
- Pure Go (no need for cgo)
- Object search/traversal.
- In-place value replacement.
- Remove object/array members.
- Serialize parsed JSONas binary data.
- Re-serialize parts as JSON.

## Requirements

There are two implementations of the parser, and which one you get depends on the platform:

| build | implementation |
|---|---|
| `amd64`, CPU with AVX2 and CLMUL | AVX2 assembly, and the AVX-512 variant where the CPU has AVX-512F |
| `amd64`, CPU without AVX2 or CLMUL | none - `SupportedCPU()` is false and `Parse` returns an error |
| `arm64` | NEON assembly for the stage 1 classification, portable Go for the rest |
| any other platform, or `-tags=noasm`, or `gccgo` | portable Go |

The portable implementation is a straight translation of the same algorithm and produces
byte for byte identical output - the whole corpus has been checked, on both
architectures, tape word for tape word. It is slower, since the step the vector units do
- turning 64 bytes into the masks the rest of stage 1 works on - becomes a table lookup
per byte.

`arm64` replaces exactly that step with NEON, in `stage1_classify_arm64.s`, and shares
everything else with the portable path: the bit arithmetic after it compiles about as
well from Go as it would from assembly, and the string parser's byte scanning already
goes through `bytes.IndexByte`, which is NEON in the runtime. The string parser has no
hand-written arm64 version, so `arm64` will not match `amd64` on string heavy documents.

The two implementations are held together by `stage1_generic_diff_amd64_test.go` and
`parse_string_generic_diff_amd64_test.go`, which run the portable code against the
assembly over the corpus on `amd64`, and by `stage1_classify_arm64_test.go`, which does
the same the other way round on `arm64`.

A CPU with both AVX2 and CLMUL is required for the fast path (Haswell from 2013 onwards should do for Intel, for AMD a Ryzen/EPYC CPU (Q1 2017) should be sufficient).
This can be checked using the provided [`SupportedCPU()`](https://pkg.go.dev/github.com/data-bet/simdjson-go?tab=doc#SupportedCPU) function.

An `amd64` CPU older than that is the one case with no fallback in the default build.
Building with `-tags=noasm` gives it the portable implementation. It is not selected
automatically because the check would have to be made per string in the hottest part of
stage 2, which measured at over 2% on string heavy documents for every other user.

Serialized data can be deserialized anywhere, on any CPU.

## Usage

Run the following command in order to install `simdjson-go`

```bash
go get -u github.com/data-bet/simdjson-go
```

In order to parse a JSON byte stream, you either call [`simdjson.Parse()`](https://pkg.go.dev/github.com/data-bet/simdjson-go?tab=doc#Parse)
or [`simdjson.ParseND()`](https://pkg.go.dev/github.com/data-bet/simdjson-go?tab=doc#ParseND) for newline delimited JSON files.
Both of these functions return a [`ParsedJson`](https://pkg.go.dev/github.com/data-bet/simdjson-go?tab=doc#ParsedJson)
struct that can be used to navigate the JSON object by calling [`Iter()`](https://pkg.go.dev/github.com/data-bet/simdjson-go?tab=doc#ParsedJson.Iter).

The easiest use is to call [`ForEach()`]((https://pkg.go.dev/github.com/data-bet/simdjson-go?tab=doc#ParsedJson.ForEach)) function of the returned `ParsedJson`.

```Go
func main() {
	// Parse JSON:
	pj, err := Parse([]byte(`{"Image":{"URL":"http://example.com/example.gif"}}`), nil)
	if err != nil {
		log.Fatal(err)
	}

	// Iterate each top level element.
	_ = pj.ForEach(func(i Iter) error {
		fmt.Println("Got iterator for type:", i.Type())
		element, err := i.FindElement(nil, "Image", "URL")
		if err == nil {
			value, _ := element.Iter.StringCvt()
			fmt.Println("Found element:", element.Name, "Type:", element.Type, "Value:", value)
		}
		return nil
	})

	// Output:
	// Got iterator for type: object
	// Found element: URL Type: string Value: http://example.com/example.gif
}
```

### Parsing with iterators

Using the type [`Iter`](https://pkg.go.dev/github.com/data-bet/simdjson-go?tab=doc#Iter) you can call
[`Advance()`](https://pkg.go.dev/github.com/data-bet/simdjson-go?tab=doc#Iter.Advance) to iterate over the tape, like so:

```Go
for {
    typ := iter.Advance()

    switch typ {
    case simdjson.TypeRoot:
        if typ, tmp, err = iter.Root(tmp); err != nil {
            return
        }

        if typ == simdjson.TypeObject {
            if obj, err = tmp.Object(obj); err != nil {
                return
            }

            e := obj.FindKey(key, &elem)
            if e != nil && elem.Type == simdjson.TypeString {
                v, _ := elem.Iter.StringBytes()
                fmt.Println(string(v))
            }
        }

    default:
        return
    }
}
```

When you advance the Iter you get the next type currently queued.

Each type then has helpers to access the data. When you get a type you can use these to access the data:

| Type       | Action on Iter             |
|------------|----------------------------|
| TypeNone   | Nothing follows. Iter done |
| TypeNull   | Null value                 |
| TypeString | `String()`/`StringBytes()` |
| TypeInt    | `Int()`/`Float()`          |
| TypeUint   | `Uint()`/`Float()`         |
| TypeFloat  | `Float()`                  |
| TypeBool   | `Bool()`                   |
| TypeObject | `Object()`                 |
| TypeArray  | `Array()`                  |
| TypeRoot   | `Root()`                   |

You can also get the next value as an `interface{}` using the [Interface()](https://pkg.go.dev/github.com/data-bet/simdjson-go#Iter.Interface) method.

Note that arrays and objects that are null are always returned as `TypeNull`.

The complex types returns helpers that will help parse each of the underlying structures.

It is up to you to keep track of the nesting level you are operating at.

For any `Iter` it is possible to marshal the recursive content of the Iter using
[`MarshalJSON()`](https://pkg.go.dev/github.com/data-bet/simdjson-go#Iter.MarshalJSON) or
[`MarshalJSONBuffer(...)`](https://pkg.go.dev/github.com/data-bet/simdjson-go#Iter.MarshalJSONBuffer).

Currently, it is not possible to unmarshal into structs.

### Search by path

It is possible to search by path to find elements by traversing objects.

For example:

```
	// Find element in path.
	elem, err := i.FindElement(nil, "Image", "URL")
```

Will locate the field inside a json object with the following structure:

```
{
    "Image": {
        "URL": "value"
    }
}
```

The values can be any type. The [Element](https://pkg.go.dev/github.com/data-bet/simdjson-go#Element)
will contain the element information and an Iter to access the content.

## Parsing Objects

If you are only interested in one key in an object you can use `FindKey` to quickly select it.

It is possible to use the `ForEach(fn func(key []byte, i Iter), onlyKeys map[string]struct{})` 
which makes it possible to get a callback for each element in the object. 

An object can be traversed manually by using `NextElement(dst *Iter) (name string, t Type, err error)`.
The key of the element will be returned as a string and the type of the value will be returned
and the provided `Iter` will contain an iterator which will allow access to the content.

There is a `NextElementBytes` which provides the same, but without the need to allocate a string.

All elements of the object can be retrieved using a pretty lightweight [`Parse`](https://pkg.go.dev/github.com/data-bet/simdjson-go#Object.Parse)
which provides a map of all keys and all elements an a slide.

All elements of the object can be returned as `map[string]interface{}` using the `Map` method on the object.
This will naturally perform allocations for all elements.

## Parsing Arrays

[Arrays](https://pkg.go.dev/github.com/data-bet/simdjson-go#Array) in JSON can have mixed types.

It is possible to call `ForEach(fn func(i Iter))` to get each element.

To iterate over the array with mixed types use the [`Iter`](https://pkg.go.dev/github.com/data-bet/simdjson-go#Array.Iter)
method to get an iterator.

There are methods that allow you to retrieve all elements as a single type,
[]int64, []uint64, []float64 and []string with AsInteger(), AsUint64(), AsFloat() and AsString().

## Number parsing

Numbers in JSON are untyped and are returned by the following rules in order:

* If there is any float point notation, like exponents, or a dot notation, it is always returned as float.
* If number is a pure integer and it fits within an int64 it is returned as such.
* If number is a pure positive integer and fits within a uint64 it is returned as such.
* If the number is valid number it is returned as float64.

If the number was converted from integer notation to a float due to not fitting inside int64/uint64
the `FloatOverflowedInteger` flag is set, which can be retrieved using `(Iter).FloatFlags()` method.

JSON numbers follow JavaScript’s double-precision floating-point format.

* Represented in base 10 with no superfluous leading zeros (e.g. 67, 1, 100).
* Include digits between 0 and 9.
* Can be a negative number (e.g. -10).
* Can be a fraction (e.g. .5).
* Can also have an exponent of 10, prefixed by e or E with a plus or minus sign to indicate positive or negative exponentiation.
* Octal and hexadecimal formats are not supported.
* Can not have a value of NaN (Not A Number) or Infinity.

## Parsing NDJSON stream

Newline delimited json is sent as packets with each line being a root element.

Here is an example that counts the number of `"Make": "HOND"` in NDJSON similar to this:

```
{"Age":20, "Make": "HOND"}
{"Age":22, "Make": "TLSA"}
```

```Go
func findHondas(r io.Reader) {
	var nFound int

	// Communication
	reuse := make(chan *simdjson.ParsedJson, 10)
	res := make(chan simdjson.Stream, 10)

	simdjson.ParseNDStream(r, res, reuse)
	// Read results in blocks...
	for got := range res {
		if got.Error != nil {
			if got.Error == io.EOF {
				break
			}
			log.Fatal(got.Error)
		}

		var result int
		var elem *Element
		err := got.Value.ForEach(func(i Iter) error {
			var err error
			elem, err = i.FindElement(elem, "Make")
			if err != nil {
				return nil
			}
			bts, _ := elem.Iter.StringBytes()
			if string(bts) == "HOND" {
				result++
			}
			return nil
		})
		reuse <- got.Value
	}
	fmt.Println("Found", nFound, "Hondas")
}
```

More examples can be found in the examples subdirectory and further documentation can be found at [godoc](https://pkg.go.dev/github.com/data-bet/simdjson-go?tab=doc).


### In-place Value Replacement

It is possible to replace a few, basic internal values.
This means that when re-parsing or re-serializing the parsed JSON these values will be output.

Boolean (true/false) and null values can be freely exchanged.

Numeric values (float, int, uint) can be exchanged freely.

Strings can also be exchanged with different values.

Strings and numbers can be exchanged. However, note that there is no checks for numbers inserted as object keys,
so if used for this invalid JSON is possible.

There is no way to modify objects, arrays, other than value types above inside each.
It is not possible to remove or add elements.

To replace a value, of value referenced by an `Iter` simply call `SetNull`, `SetBool`, `SetFloat`, `SetInt`, `SetUInt`,
`SetString` or `SetStringBytes`.

### Object & Array Element Deletion

It is possible to delete one or more elements in an object.

`(*Object).DeleteElems(fn, onlyKeys)` will call back fn for each key+ value.

If true is returned, the key+value is deleted. A key filter can be provided for optional filtering.
If the callback function is nil all elements matching the filter will be deleted.
If both are nil all elements are deleted.

Example:

```Go
	// The object we are modifying
	var obj *simdjson.Object

	// Delete all entries where the key is "unwanted":
	err = obj.DeleteElems(func(key []byte, i Iter) bool {
		return string(key) == "unwanted")
	}, nil)

	// Alternative version with prefiltered keys:
	err = obj.DeleteElems(nil, map[string]struct{}{"unwanted": {}})
```

`(*Array).DeleteElems(fn func(i Iter) bool)` will call back fn for each array value.
If the function returns true the element is deleted in the array.

```Go
	// The array we are modifying
	var array *simdjson.Array

	// Delete all entries that are strings.
	array.DeleteElems(func(i Iter) bool {
		return i.Type() == TypeString
	})
```

## Serializing parsed json

It is possible to serialize parsed JSON for more compact storage and faster load time.

To create a new serialized use [NewSerializer](https://pkg.go.dev/github.com/data-bet/simdjson-go#NewSerializer).
This serializer can be reused for several JSON blocks.

The serializer will provide string deduplication and compression of elements.
This can be finetuned using the [`CompressMode`](https://pkg.go.dev/github.com/data-bet/simdjson-go#Serializer.CompressMode) setting.

To serialize a block of parsed data use the [`Serialize`](https://pkg.go.dev/github.com/data-bet/simdjson-go#Serializer.Serialize) method.

To read back use the [`Deserialize`](https://pkg.go.dev/github.com/data-bet/simdjson-go#Serializer.Deserialize) method.
For deserializing the compression mode does not need to match since it is read from the stream.

Speed and size on two documents from the test corpus, `twitter` (mostly strings) and
`canada` (mostly floats), medians of three one-second runs on the machine named under
[Performance vs `encoding/json`](#performance-vs-encodingjson). Reproduce with
`go test -run '^$' -bench 'BenchmarkSerialize$|BenchmarkDeSerialize$'`:

| Compress Mode | twitter: % of JSON | serialize | deserialize | canada: % of JSON | serialize | deserialize |
|---|---|---|---|---|---|---|
| None    | 69.9% | 3211 MB/s | 14777 MB/s | 69.3% | 4804 MB/s | 6789 MB/s |
| Fast    | 11.0% | 1986 MB/s |  8625 MB/s | 36.2% | 1586 MB/s | 2887 MB/s |
| Default | 10.6% | 1571 MB/s |  8645 MB/s | 29.7% |  770 MB/s | 2067 MB/s |
| Best    |  7.3% | 1042 MB/s |  7947 MB/s | 24.2% |  501 MB/s | 1822 MB/s |

String deduplication is what makes the difference between the two documents: `twitter`
repeats a modest vocabulary and compresses to a tenth, while `canada` is two million
distinct coordinates and has nothing to deduplicate. The earlier version of this table was
measured on `parking-citations-1M`, which the asset host no longer serves, so it could not
be reproduced.

## Performance vs `encoding/json`

Parsing the benchmark corpus, against `encoding/json` unmarshalling the same documents into
an `interface{}`. Both columns are medians of six one-second runs, taken with the parser
warm - `Parse` reusing a `ParsedJson`, which is how a service would call it.

| document | `encoding/json` | simdjson, copy | simdjson, nocopy | faster by |
|---|---|---|---|---|
| apache_builds | 694 µs, 183 MB/s | 57 µs, 2225 MB/s | 56 µs, 2277 MB/s | 12.1x |
| canada | 16.00 ms, 141 MB/s | 2.89 ms, 778 MB/s | 2.89 ms, 778 MB/s | 5.5x |
| citm_catalog | 9.03 ms, 191 MB/s | 479 µs, 3603 MB/s | 471 µs, 3670 MB/s | 18.8x |
| github_events | 339 µs, 192 MB/s | 25 µs, 2614 MB/s | 25 µs, 2582 MB/s | 13.6x |
| gsoc-2018 | 12.36 ms, 269 MB/s | 445 µs, 7476 MB/s | 527 µs, 6317 MB/s | 27.7x |
| instruments | 1.39 ms, 158 MB/s | 121 µs, 1825 MB/s | 120 µs, 1833 MB/s | 11.5x |
| marine_ik | 25.45 ms, 117 MB/s | 3.21 ms, 928 MB/s | 3.22 ms, 926 MB/s | 7.9x |
| mesh | 6.30 ms, 115 MB/s | 1.01 ms, 719 MB/s | 1.01 ms, 718 MB/s | 6.3x |
| mesh.pretty | 9.21 ms, 171 MB/s | 1.13 ms, 1393 MB/s | 1.13 ms, 1394 MB/s | 8.1x |
| numbers | 1.02 ms, 147 MB/s | 216 µs, 694 MB/s | 217 µs, 693 MB/s | 4.7x |
| random | 3.62 ms, 141 MB/s | 323 µs, 1580 MB/s | 316 µs, 1616 MB/s | 11.2x |
| twitter | 3.79 ms, 167 MB/s | 221 µs, 2863 MB/s | 224 µs, 2824 MB/s | 17.2x |
| twitterescaped | 3.56 ms, 158 MB/s | 336 µs, 1676 MB/s | 459 µs, 1225 MB/s | 10.6x |
| update-center | 3.59 ms, 149 MB/s | 268 µs, 1993 MB/s | 266 µs, 2004 MB/s | 13.4x |

Geometric mean 10.8x, from 4.7x on an array of numbers to 27.7x on a large string-heavy
document. The two ends of that range are the honest summary: the more of a document is
strings and structure, the more there is for the vector code to do, and `numbers` is
almost entirely `strconv`-shaped work that both libraries have to do byte by byte.

Allocation is the other half, and the gap is larger:

| document | `encoding/json` | simdjson, copy |
|---|---|---|
| apache_builds | 0.46 MB, 9719 allocs | 31 B, 1 alloc |
| canada | 10.58 MB, 392514 allocs | 7.5 kB, 3 allocs |
| citm_catalog | 5.13 MB, 95864 allocs | 1.2 kB, 3 allocs |
| github_events | 0.18 MB, 3365 allocs | 21 B, 1 alloc |
| gsoc-2018 | 7.03 MB, 58605 allocs | 3.1 kB, 3 allocs |
| instruments | 0.87 MB, 13709 allocs | 62 B, 1 alloc |
| marine_ik | 21.88 MB, 614774 allocs | 38 kB, 3 allocs |
| mesh | 6.56 MB, 149495 allocs | 3.0 kB, 3 allocs |
| mesh.pretty | 6.71 MB, 149495 allocs | 2.2 kB, 3 allocs |
| numbers | 0.91 MB, 20023 allocs | 81 B, 1 alloc |
| random | 2.81 MB, 68019 allocs | 484 B, 3 allocs |
| twitter | 2.08 MB, 32124 allocs | 248 B, 3 allocs |
| twitterescaped | 2.26 MB, 32620 allocs | 502 B, 3 allocs |
| update-center | 2.71 MB, 50810 allocs | 251 B, 3 allocs |

`encoding/json` allocates a Go value per element; simdjson-go appends to buffers a reused
`ParsedJson` already owns. On a cold parse - a fresh `ParsedJson` every time - it allocates
those buffers instead, which for these documents is between 1 MB and 14 MB.

**What this comparison is not.** `Parse` builds a tape and stops; `Unmarshal` materialises
every element as a Go value. Reading the tape afterwards costs more, and a caller who needs
all of the document as Go values will not see 10x. `benchmarks/BenchmarkMaterialise` asks
both sides for the same thing, and there the same corpus comes out at 2.7x with sonic
roughly level with simdjson-go; `benchmarks/README.md` lists every asymmetry between the
libraries and which of them were equalised. Compare within one group, never across.

**How these were taken.** Go 1.26.0, linux/amd64, AMD Ryzen AI MAX+ 395, both columns
pinned to two physical cores that share an L3 slice. To reproduce:

```sh
# simdjson-go, both copy modes, from the repository root
taskset -c 2,3 go test -run '^$' -bench BenchmarkParseCorpus -benchtime 1s -count 6

# encoding/json and sonic, from benchmarks/
taskset -c 2,3 go test -run '^$' -bench 'BenchmarkEncodingJson|BenchmarkSonic' \
    -benchtime 1s -count 6
```

Numbers from a different machine will differ; the ratios travel better than the absolutes,
and the shape - which documents gain most - travels best of all.

### Inplace strings

The best performance is obtained by keeping the JSON message fully mapped in memory and using the
`WithCopyStrings(false)` option. This prevents duplicate copies of string values being made
but mandates that the original JSON buffer is kept alive until the `ParsedJson` object is no longer needed
(ie iteration over the tape format has been completed).

In case the JSON message buffer is freed earlier (or for streaming use cases where memory is reused)
`WithCopyStrings(true)` should be used (which is the default behaviour).

The two are much closer than they used to be. Copying a string no longer means walking it a
second time - the copy and the search for the closing quote are one pass, and a string with
no escapes is copied with `append` rather than word by word - so the difference is now the
memory traffic of the copy itself:

| document | copy | nocopy | difference |
|---|---|---|---|
| apache_builds | 2225 MB/s | 2277 MB/s | -2% |
| citm_catalog | 3603 MB/s | 3670 MB/s | -2% |
| github_events | 2614 MB/s | 2582 MB/s | +1% |
| gsoc-2018 | 7476 MB/s | 6317 MB/s | +18% |
| twitter | 2863 MB/s | 2824 MB/s | +1% |
| twitterescaped | 1676 MB/s | 1225 MB/s | +37% |
| update-center | 1993 MB/s | 1993 MB/s | 0% |
| canada, mesh, numbers | \~equal | \~equal | 0% |

Two of those want explaining, since they say copying is *faster*: `twitterescaped` and
`gsoc-2018` are the documents where nearly every string carries escapes, and with
`WithCopyStrings(false)` such a string still has to be decoded somewhere - it goes to the
string buffer anyway - while the branch that avoids the copy entirely never fires. What the
option really saves is the copy of strings that need no decoding, which is why the
documents with plain strings show a couple of percent and the ones with none at all
(canada, mesh, numbers) show nothing.

So `WithCopyStrings(false)` is worth reaching for when the message will outlive the parse
anyway. It is not worth restructuring a service around.

## Design

`simdjson-go` follows the same two stage design as `simdjson`.
During the first stage the structural elements (`{`, `}`, `[`, `]`, `:`, and `,`)
are detected and forwarded as offsets in the message buffer to the second stage.
The second stage builds a tape format of the structure of the JSON document.

Note that in contrast to `simdjson`, `simdjson-go` outputs `uint32`
increments (as opposed to absolute values) to the second stage.
This allows arbitrarily large JSON files to be parsed (as long as a single (string) element does not surpass 4 GB...).

Also, for better performance,
both stages run concurrently as separate go routines and a go channel is used to communicate between the two stages.

### Stage 1

Stage 1 was originally converted from C code containing the SIMD intrinsics to Golang assembly using [c2goasm](https://github.com/minio/c2goasm).
That C code is no longer part of this repository, and the assembly has been maintained by hand since - the AVX-512 variants in particular were written directly - so the `.s` files, not any generator, are the definition of what stage 1 does.
It essentially consists of five separate steps, being:

- `find_odd_backslash_sequences`: detect backslash characters used to escape quotes
- `find_quote_mask_and_bits`: generate a mask with bits turned on for characters between quotes
- `find_whitespace_and_structurals`: generate a mask for whitespace plus a mask for the structural characters
- `finalize_structurals`: combine the masks computed above into a final mask where each active bit represents the position of a structural character in the input message.
- `flatten_bits_incremental`: output the active bits in the final mask as incremental offsets.

For more details you can take a look at the various test cases in `find_subroutines_amd64_test.go` to see how
the individual routines can be invoked (typically with a 64 byte input buffer that generates one or more 64-bit masks).

There is one final routine, `find_structural_bits_in_slice`, that ties it all together and is
invoked with a slice of the message buffer in order to find the incremental offsets.

### Stage 2

During Stage 2 the tape structure is constructed.
It is essentially a single function that jumps around as it finds the various structural characters
and builds the hierarchy of the JSON document that it processes.
The values of the JSON elements such as strings, integers, booleans etc. are parsed and written to the tape.

Any errors (such as an array not being closed or a missing closing brace) are detected and reported back as errors to the client.

## Tape format

Similarly to `simdjson`, `simdjson-go` parses the structure onto a 'tape' format.
With this format it is possible to skip over arrays and (sub)objects as the sizes are recorded in the tape.

`simdjson-go` format is exactly the same as the `simdjson` [tape](https://github.com/lemire/simdjson/blob/master/doc/tape.md)
format with the following 2 exceptions:

- In order to support ndjson, it is possible to have more than one root element on the tape.
Also, to allow for fast navigation over root elements, a root points to the next root element
(and as such the last root element points 1 index past the length of the tape).

A "NOP" tag is added. The value contains the number of tape entries to skip forward for next tag.

- Strings are handled differently, unlike `simdjson` the string size is not prepended in the String buffer
but is added as an additional element to the tape itself (much like integers and floats).
  - In case `WithCopyStrings(false)` Only strings that contain special characters are copied to the String buffer
in which case the payload from the tape is the offset into the String buffer.
For string values without special characters the tape's payload points directly into the message buffer.
  - In case `WithCopyStrings(true)` (default): Strings are always copied to the String buffer.

For more information, see `TestStage2BuildTape` in `stage2_build_tape_test.go`.

## Fuzz Tests

`simdjson-go` has been extensively fuzz tested to ensure that input cannot generate crashes and that output matches
the standard library.

The fuzz tests are included as Go 1.18+ compatible tests.

## License

`simdjson-go` is released under the Apache License v2.0. You can find the complete text in the file LICENSE.

## Contributing

Contributions are welcome, please send PRs for any enhancements.

If your PR include parsing changes please run fuzz testers for a couple of hours.
