/*
 * MinIO Cloud Storage, (C) 2020 MinIO, Inc.
 * Modifications copyright (c) 2026 DATA.BET
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
	"fmt"
	"math/bits"
	"strings"
	"testing"
)

// TestStage1FindMarksGeneric asserts, of the portable stage 1, exactly what
// TestStage1FindMarks asserts of the assembly: the four masks one block of demo_json
// produces, written out a bit at a time. The two tests share the expectations rather
// than one deriving them from the other, so a mask that is wrong in the same way in
// both implementations still fails here.
func TestStage1FindMarksGeneric(t *testing.T) {
	const (
		wantQuoted     = "0111111000111111000000111111100000011111100111111111111111111111"
		wantStructural = "1000000011000000010001000000001000100000001000000000000000000000"
		wantWhitespace = "0000000000000000000000000000000000000000000000001000010000100000"
		wantFinalized  = "1100000011100000011001100000001100110000001100000000000000000000"
	)

	block := (*[64]byte)([]byte(demo_json)[:64])
	backslash, quote, whitespace, structural, _, _ := classifyBlockGeneric(block)

	prevOddBackslash := uint64(0)
	oddEnds := findOddBackslashSequencesGeneric(backslash, &prevOddBackslash)
	if oddEnds != 0 {
		t.Errorf("odd backslash ends: got %d, want 0", oddEnds)
	}

	quoteBits := quote &^ oddEnds
	prevInsideQuote := uint64(0)
	quoteMask := prefixXOR(quoteBits) ^ prevInsideQuote

	for _, c := range []struct {
		name string
		got  uint64
		want string
	}{
		{"quoted", quoteMask, wantQuoted},
		{"structurals", structural, wantStructural},
		{"whitespace", whitespace, wantWhitespace},
	} {
		if s := fmt.Sprintf("%064b", bits.Reverse64(c.got)); s != c.want {
			t.Errorf("%s: got: %s want: %s", c.name, s, c.want)
		}
	}

	prevPseudoPred := uint64(0)
	finalized := finalizeStructuralsGeneric(structural, whitespace, quoteMask, quoteBits, &prevPseudoPred)
	if s := fmt.Sprintf("%064b", bits.Reverse64(finalized)); s != wantFinalized {
		t.Errorf("structurals finalized: got: %s want: %s", s, wantFinalized)
	}
}

func TestFindStructuralIndices(t *testing.T) {
	if !SupportedCPU() {
		t.SkipNow()
	}
	parsed := []string{
		`{"Image":{"Width":800,"Height":600,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		` "Image":{"Width":800,"Height":600,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`        :{"Width":800,"Height":600,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`         {"Width":800,"Height":600,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`          "Width":800,"Height":600,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                 :800,"Height":600,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                  800,"Height":600,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                     ,"Height":600,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                      "Height":600,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                              :600,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                               600,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                  ,"Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                   "Title":"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                          :"View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                           "View from 15th Floor","Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                 ,"Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                  "Thumbnail":{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                             :{"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                              {"Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                               "Url":"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                    :"http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                     "http://www.example.com/image/481989943","Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                             ,"Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                              "Height":125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                      :125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                       125,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                          ,"Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                           "Width":100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                                  :100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                                   100},"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                                      },"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                                       ,"Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                                        "Animated":false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                                                  :false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                                                   false,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                                                        ,"IDs":[116,943,234,38793]}}`,
		`                                                                                                                                                                         "IDs":[116,943,234,38793]}}`,
		`                                                                                                                                                                              :[116,943,234,38793]}}`,
		`                                                                                                                                                                               [116,943,234,38793]}}`,
		`                                                                                                                                                                                116,943,234,38793]}}`,
		`                                                                                                                                                                                   ,943,234,38793]}}`,
		`                                                                                                                                                                                    943,234,38793]}}`,
		`                                                                                                                                                                                       ,234,38793]}}`,
		`                                                                                                                                                                                        234,38793]}}`,
		`                                                                                                                                                                                           ,38793]}}`,
		`                                                                                                                                                                                            38793]}}`,
		`                                                                                                                                                                                                 ]}}`,
		`                                                                                                                                                                                                  }}`,
		`                                                                                                                                                                                                   }`,
	}

	pj := internalParsedJson{}
	// Drives stage 1 on its own, so it wants the channel hand-off rather than the
	// collect-into-a-slice path parseMessage uses for small inputs.
	pj.async = true
	pj.indexChans = make(chan indexChan, 16)

	// No need to spawn go-routine since the channel is large enough
	pj.Message = []byte(demo_json)
	pj.findStructuralIndices()

	ipos, pos := 0, ^uint64(0)
	for ic := range pj.indexChans {
		if ic.index == -1 {
			break
		}
		for j := 0; j < ic.length; j++ {
			pos += uint64((*ic.indexes)[j])
			result := fmt.Sprintf("%s%s", strings.Repeat(" ", int(pos)), demo_json[pos:])
			// fmt.Printf("`%s`,\n", result)
			if result != parsed[ipos] {
				t.Errorf("TestFindStructuralBits: got: %s want: %s", result, parsed[ipos])
			}
			ipos++
		}
	}
}

func BenchmarkStage1(b *testing.B) {
	if !SupportedCPU() {
		b.SkipNow()
	}
	msg := loadCompressed(b, "twitter")

	b.SetBytes(int64(len(msg)))
	b.ReportAllocs()
	b.ResetTimer()

	pj := internalParsedJson{}
	pj.async = true // stage 1 alone, so hand buffers over the channel
	pj.Message = msg
	for i := 0; i < b.N; i++ {
		// Create new channel (large enough so we won't block)
		pj.indexChans = make(chan indexChan, 128)
		pj.findStructuralIndices()
	}
}
