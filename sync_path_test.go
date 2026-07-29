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
	"bytes"
	"fmt"
	"testing"
)

// denseArray builds `[1,1,...]` of about n bytes. Every element contributes two
// structural indexes per two bytes of input, which is the densest a valid document
// gets and so the worst case for how many index buffers stage 1 needs.
func denseArray(n int) []byte {
	k := (n - 3) / 2 // "[" + k*"1," + "1]" is 2k+3 bytes
	if k < 1 {
		k = 1
	}
	b := make([]byte, 0, 2*k+3)
	b = append(b, '[')
	for i := 0; i < k; i++ {
		b = append(b, '1', ',')
	}
	b = append(b, '1', ']')
	return b
}

func countArrayElems(t *testing.T, pj *ParsedJson) int {
	t.Helper()
	i := pj.Iter()
	if typ := i.Advance(); typ != TypeRoot {
		t.Fatalf("first element is %v, want root", typ)
	}
	var tmp Iter
	typ, iter, err := i.Root(&tmp)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if typ != TypeArray {
		t.Fatalf("root holds %v, want array", typ)
	}
	arr, err := iter.Array(nil)
	if err != nil {
		t.Fatalf("Array: %v", err)
	}
	vals, err := arr.AsInteger()
	if err != nil {
		t.Fatalf("AsInteger: %v", err)
	}
	return len(vals)
}

// The synchronous path has no channel throttling stage 1, so every buffer it fills
// has to remain live at once - which is why it takes them from syncBuffer rather than
// the ring. Check that the densest document below the cutoff gets a distinct buffer
// for every round, and that it really does need more than the ring holds.
func TestSyncPathMultipleIndexBuffers(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	msg := denseArray(syncSizeLimit)
	want := bytes.Count(msg, []byte{','}) + 1

	pj := &internalParsedJson{}
	pj.copyStrings = true
	if err := pj.parseMessage(msg, false); err != nil {
		t.Fatalf("parseMessage(%d bytes): %v", len(msg), err)
	}
	if pj.async {
		t.Fatalf("%d byte message took the async path", len(msg))
	}
	if len(pj.indexList) <= indexSlots {
		t.Fatalf("stage 1 used %d index buffer(s), within the %d of the ring; the case syncBuffer exists for is not covered",
			len(pj.indexList), indexSlots)
	}
	seen := make(map[*[indexSize]uint32]int, len(pj.indexList))
	for i, ic := range pj.indexList {
		if first, dup := seen[ic.indexes]; dup {
			t.Fatalf("index buffers %d and %d are the same one, so they overwrote each other", first, i)
		}
		seen[ic.indexes] = i
	}
	t.Logf("%d bytes, %d distinct index buffers (%d from the ring)", len(msg), len(pj.indexList), indexSlots)

	if got := countArrayElems(t, &pj.ParsedJson); got != want {
		t.Errorf("parsed %d elements, want %d", got, want)
	}
}

// Sizes either side of the cutoff go down different paths and must agree. Comparing
// the re-serialized form against the input catches an index dropped or repeated at a
// buffer boundary, which is where the two paths differ.
func TestSyncAsyncBoundary(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	sawSync, sawAsync := false, false
	for n := syncSizeLimit - 300; n <= syncSizeLimit+300; n += 7 {
		msg := denseArray(n)
		pj := &internalParsedJson{}
		pj.copyStrings = true
		if err := pj.parseMessage(msg, false); err != nil {
			t.Fatalf("parseMessage(%d bytes): %v", len(msg), err)
		}
		if pj.async {
			sawAsync = true
		} else {
			sawSync = true
		}

		it := pj.ParsedJson.Iter()
		out, err := it.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON(%d bytes): %v", len(msg), err)
		}
		if !bytes.Equal(out, msg) {
			t.Fatalf("%d bytes, async=%v: round trip differs\n got %s\nwant %s",
				len(msg), pj.async, head(out), head(msg))
		}
	}
	if !sawSync || !sawAsync {
		t.Fatalf("the range did not straddle the cutoff: sync=%v async=%v", sawSync, sawAsync)
	}
}

func head(b []byte) string {
	if len(b) > 60 {
		return fmt.Sprintf("%s...(%d bytes)", b[:60], len(b))
	}
	return string(b)
}
