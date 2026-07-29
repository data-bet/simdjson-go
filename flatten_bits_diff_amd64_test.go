//go:build !noasm && !appengine && gc
// +build !noasm,!appengine,gc

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

package simdjson

import (
	"math/bits"
	"math/rand"
	"reflect"
	"testing"
)

// flattenBitsReference spells out what the assembly is supposed to do, so the two
// can be diffed over far more input than a table of cases covers.
func flattenBitsReference(out []uint32, mask uint64, carried *int, position *uint64) []uint32 {
	if mask == 0 {
		*carried += 64
		return out
	}
	prev := -(*carried + 1)
	last := 0
	for m := mask; m != 0; m &= m - 1 {
		p := bits.TrailingZeros64(m)
		out = append(out, uint32(p-prev))
		*position += uint64(p - prev)
		prev = p
		last = p
	}
	*carried = 63 - last
	return out
}

func TestFlattenBitsDiff(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	rng := rand.New(rand.NewSource(2))

	// A mix of densities: sparse masks exercise the carry across empty blocks, dense
	// ones the inner loop, and the single-bit sweep every bit position.
	var masks []uint64
	for i := 0; i < 64; i++ {
		masks = append(masks, 1<<uint(i))
	}
	masks = append(masks, 0, ^uint64(0), 1, 1<<63, 3, 3<<62)
	for i := 0; i < 4000; i++ {
		switch i % 4 {
		case 0:
			masks = append(masks, rng.Uint64())
		case 1:
			masks = append(masks, rng.Uint64()&rng.Uint64()&rng.Uint64()) // sparse
		case 2:
			masks = append(masks, rng.Uint64()|rng.Uint64()) // dense
		case 3:
			masks = append(masks, 0) // empty, forces a carry
		}
	}

	// Run the whole sequence through both, restarting every so often so that a wide
	// range of carried values is reached.
	for start := 0; start < len(masks); start += 37 {
		end := start + 200
		if end > len(masks) {
			end = len(masks)
		}
		seq := masks[start:end]

		index := indexChan{indexes: &[indexSize]uint32{}}
		gotCarried := 0
		gotPosition := ^uint64(0)

		var want []uint32
		wantCarried := 0
		wantPosition := ^uint64(0)

		for _, mask := range seq {
			if index.length+64 >= indexSize {
				break
			}
			flatten_bits_incremental(index.indexes, &index.length, mask, &gotCarried, &gotPosition)
			want = flattenBitsReference(want, mask, &wantCarried, &wantPosition)

			if index.length != len(want) {
				t.Fatalf("start=%d mask=%#x: got %d indexes, want %d", start, mask, index.length, len(want))
			}
			if gotCarried != wantCarried {
				t.Fatalf("start=%d mask=%#x: carried = %d, want %d", start, mask, gotCarried, wantCarried)
			}
			if gotPosition != wantPosition {
				t.Fatalf("start=%d mask=%#x: position = %d, want %d", start, mask, gotPosition, wantPosition)
			}
		}
		if got := index.indexes[:index.length]; !reflect.DeepEqual(got, want) {
			t.Fatalf("start=%d: indexes differ\n got %v\nwant %v", start, got, want)
		}
	}
}
