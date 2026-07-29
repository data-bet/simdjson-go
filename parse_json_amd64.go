//go:build !noasm && !appengine && gc
// +build !noasm,!appengine,gc

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
	"bytes"
	"errors"
	"sync"
)

// syncSizeLimit is the largest message stage 1 and stage 2 handle one after the
// other rather than concurrently. See internalParsedJson.async for what bounds it.
const syncSizeLimit = 8 << 10

func (pj *internalParsedJson) initialize(size int) {
	// Estimate the tape size to be about 15% of the length of the JSON message.
	// Across the test corpus the real figure runs from 2.3% (string heavy) to 21.6%
	// (arrays of numbers, where two tape entries can cover as little as two bytes of
	// input), so this is short for the densest files and generous for the sparsest.
	// Raising it to cover everything was measured and rejected: it bought nothing
	// beyond noise on the dense files and cost citm_catalog 12.7% on a cold parse,
	// because reserving tape it never fills is not free either.
	avgTapeSize := size * 15 / 100
	if cap(pj.Tape) < avgTapeSize {
		pj.Tape = make([]uint64, 0, avgTapeSize)
	}
	pj.Tape = pj.Tape[:0]

	stringsSize := size / 10
	if stringsSize < 128 {
		stringsSize = 128 // always allocate at least 128 for the string buffer
	}
	if pj.Strings != nil && cap(pj.Strings.B) >= stringsSize {
		pj.Strings.B = pj.Strings.B[:0]
	} else {
		pj.Strings = &TStrings{make([]byte, 0, stringsSize)}
	}
	if cap(pj.containingScopeOffset) < maxdepth {
		pj.containingScopeOffset = make([]uint64, 0, maxdepth)
	}
	pj.containingScopeOffset = pj.containingScopeOffset[:0]
	pj.indexList = pj.indexList[:0]
}

func (pj *internalParsedJson) parseMessage(msg []byte, ndjson bool) (err error) {
	// Cache message so we can point directly to strings
	// TODO: Find out why TestVerifyTape/instruments fails without bytes.TrimSpace
	pj.Message = bytes.TrimSpace(msg)
	pj.initialize(len(pj.Message))

	if ndjson {
		pj.ndjson = 1
	} else {
		pj.ndjson = 0
	}

	pj.buffersOffset = ^uint64(0)

	var errStage1 error

	// Do long inputs async
	pj.async = len(pj.Message) > syncSizeLimit
	if pj.async {
		// Make the capacity of the channel smaller than the number of slots.
		// This way the sender will automatically block until the consumer
		// has finished the slot it is working on.
		if pj.indexChans == nil {
			pj.indexChans = make(chan indexChan, indexSlots-2)
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, done := pj.unifiedMachine(); !ok {
				err = errors.New("Bad parsing while executing stage 2")
				// Keep consuming...
				if !done {
					for idx := range pj.indexChans {
						if idx.index == -1 {
							break
						}
					}
				}
			}
		}()
		if !pj.findStructuralIndices() {
			errStage1 = errors.New("Failed to find all structural indices for stage 1")
		}
		wg.Wait()
	} else {
		// Stage 1 collects into pj.indexList, so there is no channel to hand over and
		// nothing to drain on the way out.
		if !pj.findStructuralIndices() {
			return errors.New("Failed to find all structural indices for stage 1")
		}
		if ok, _ := pj.unifiedMachine(); !ok {
			return errors.New("Bad parsing while executing stage 2")
		}
		return nil
	}

	if errStage1 != nil {
		return errStage1
	}
	return
}
