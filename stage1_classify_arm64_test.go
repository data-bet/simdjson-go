//go:build arm64 && !noasm && !appengine && gc
// +build arm64,!noasm,!appengine,gc

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
	"math/rand"
	"testing"
)

// classifyBlockNEON is the only assembly on arm64, and the portable
// classifyBlockGeneric is what it has to match: everything downstream of the two is
// shared code, so agreeing here is the whole of agreeing.
//
// This is the arm64 counterpart of TestGenericClassificationMatchesAsm, which does the
// same job the other way round on amd64. Between them the portable implementation is
// pinned to the assembly on both architectures, and so the two architectures to each
// other.
func TestNEONClassificationMatchesGeneric(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	names := [6]string{"backslash", "quote", "whitespace", "structural", "control", "newline"}

	check := func(block *[64]byte) {
		t.Helper()
		var got, want [6]uint64
		classifyBlockNEON(block, &got)
		want[0], want[1], want[2], want[3], want[4], want[5] = classifyBlockGeneric(block)
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("block %q: %s mask %#016x, want %#016x",
					block[:], names[i], got[i], want[i])
			}
		}
	}

	for c := 0; c < 256; c++ {
		var block [64]byte
		// A block of one repeated value pins the value itself; the random blocks
		// then re-test it at one position among unrelated bytes, which is what
		// catches an error confined to a single lane or to one of the four input
		// vectors.
		for i := range block {
			block[i] = byte(c)
		}
		check(&block)

		for trial := 0; trial < 8; trial++ {
			for i := range block {
				block[i] = byte(rng.Intn(256))
			}
			block[rng.Intn(64)] = byte(c)
			check(&block)
		}
	}

	// Every single-byte position, so a mask assembled in the wrong lane order shows
	// up as a bit in the wrong place rather than only as a wrong popcount.
	for _, c := range []byte{'"', '\\', '\n', ' ', '{', ':', 0x00, 0x1f, 0x7f, 0x80, 0xff} {
		for pos := 0; pos < 64; pos++ {
			var block [64]byte
			for i := range block {
				block[i] = 'a'
			}
			block[pos] = c
			check(&block)
		}
	}
}
