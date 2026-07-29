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
	"encoding/binary"
	"fmt"
)

// Constants for "return address" modes
const retAddressShift = 2
const retAddressStartConst = 1
const retAddressObjectConst = 2
const retAddressArrayConst = 3

// indexCursor walks the structural indexes stage 1 produces.
//
// It is kept in a local of unifiedMachine rather than on the parser so that the
// position and the slice stay in registers. Reading them back out of the
// heap-allocated parser cost four loads and a store per structural character, and
// the store-to-load forwarding on the position serialized one element against the
// next; it measured at 6.5-8.7% of parse time.
//
// Holding the remaining indexes as a slice rather than a pointer to the whole array
// also lets the bounds check go: the branch that refills establishes that pos is
// inside it.
type indexCursor struct {
	idxs []uint32
	pos  int
	buf  int // next entry of pj.indexList, when stage 1 ran to completion first
}

// next returns the offset of the next structural character in the message, taking
// the next buffer of indexes from stage 1 when the current one runs out. done
// reports that stage 1 has no more to give.
//
// The refill is a loop rather than a single step so that an empty buffer is skipped:
// stage 1 can emit one, having stripped a trailing quote back for the next round to
// re-emit, and there would otherwise be no index to read.
func (c *indexCursor) next(pj *internalParsedJson, idxIn uint64) (done bool, idx uint64) {
	for c.pos >= len(c.idxs) {
		var ic indexChan
		if pj.async {
			ic = <-pj.indexChans
			if ic.index == -1 {
				return true, idxIn
			}
		} else {
			if c.buf >= len(pj.indexList) {
				return true, idxIn
			}
			ic = pj.indexList[c.buf]
			c.buf++
		}
		c.idxs, c.pos = ic.indexes[ic.index:ic.length], 0
	}
	idx = idxIn + uint64(c.idxs[c.pos])
	c.pos++
	return false, idx
}

// peek returns the increment from the current structural character to the one after
// it, without consuming anything.
//
// It answers 0 when the next buffer has not been received yet. That should not
// happen where it matters - stage 1 holds a trailing quote back for the next buffer
// so a string is never the last index in one - and parseString clamps a zero away.
func (c *indexCursor) peek() uint64 {
	if c.pos >= len(c.idxs) {
		return 0
	}
	return uint64(c.idxs[c.pos])
}

// Handy "debug" method to see where Stage 2 fails (call in place of `next`)
func (c *indexCursor) nextDebug(pj *internalParsedJson, idxIn uint64) (done bool, idx uint64) {
	done, idx = c.next(pj, idxIn)
	if !done {
		fmt.Printf("At 0x%x char: %s\n", idx, string(pj.Message[idx]))
	}
	return done, idx
}

// pushScope records the current tape location for the scope we are entering,
// tagged with the state to return to when it closes. It reports false once
// maxdepth is reached, so that deeply nested input is rejected instead of
// growing containingScopeOffset without bound.
//
// initialize guarantees a capacity of at least maxdepth, so the depth check
// doubles as the bounds check and the stack is filled in place - no append, and
// therefore no growslice call or write barrier in the hot path.
func (pj *internalParsedJson) pushScope(retAddress uint64) bool {
	depth := len(pj.containingScopeOffset)
	if depth >= maxdepth {
		return false
	}
	pj.containingScopeOffset = pj.containingScopeOffset[:depth+1]
	pj.containingScopeOffset[depth] = (pj.get_current_loc() << retAddressShift) | retAddress
	return true
}

// reserveStrings makes room for n more bytes in the string buffer and returns the
// offset the caller may start writing at. Callers handing the buffer to assembly
// must include the 32 byte overshoot of the copy loop in n.
// idx is how far into the message the value being stored starts, which lets the
// growth extrapolate the document's string density instead of guessing.
func (pj *ParsedJson) reserveStrings(n, idx uint64) int {
	strs := pj.Strings.B
	requiredLen := uint64(len(strs)) + n
	if requiredLen >= uint64(cap(strs)) {
		// The message length is a hard ceiling: strings are disjoint pieces of it and
		// decoding never makes one longer, so the buffer can never be asked for more.
		limit := uint64(len(pj.Message)) + 32
		// Below that, project what the rest of the message will add at the density
		// seen so far and leave a quarter as margin. Doubling alone is too timid for a
		// document that is mostly strings - it used to take five reallocations and
		// several megabytes of garbage to get there - and jumping straight to the
		// ceiling is too eager for one that is mostly numbers.
		newSize := requiredLen * 2
		if idx > 0 {
			if projected := requiredLen * uint64(len(pj.Message)) / idx; projected > newSize {
				newSize = projected + projected/4
			}
		}
		if newSize > limit {
			newSize = limit
		}
		if newSize < requiredLen {
			newSize = requiredLen + n
		}
		grown := make([]byte, len(strs), newSize)
		copy(grown, strs)
		pj.Strings.B = grown
	}
	return len(pj.Strings.B)
}

func (pj *internalParsedJson) parseString(idx uint64, maxStringSize uint64) bool {
	needCopy := pj.copyStrings
	size := uint64(0)
	buf := pj.Message[idx:]
	// A non-zero bound is one stage 1 actually gave us, which says more than its
	// value: the next structural character only exists once stage 1 has walked past
	// the closing quote, so the closing quote is certainly within it. That is what
	// lets the fused path below run without a bound of its own.
	boundKnown := maxStringSize != 0
	// Clamp the scan bound to what is actually left in the message. The unsigned
	// wrap-around also catches maxStringSize == 0, which indexCursor.peek returns when
	// the next structural index has not been received yet: stage 1 keeps a trailing
	// quote back for the next index buffer so that should not happen for a string, but a
	// zero bound must never reach the assembly, which reads it as "no room at all" and
	// fails immediately.
	if maxStringSize-1 >= uint64(len(buf)) {
		maxStringSize = uint64(len(buf))
	}

	// The scan reads a whole YMM word at a time and only re-checks its bound
	// between words, so it can look up to 31 bytes past maxStringSize. When the
	// message does not have that much left, hand it a padded copy instead. The
	// tail must be zeroed: a stray quote there would be taken for the closing one.
	// Only maxStringSize+64 bytes are ever touched, so that is all we copy, and the
	// buffer is kept on the parser to be reused by later strings.
	if len(buf)-int(maxStringSize) < 64 {
		need := int(maxStringSize) + 64
		if cap(pj.padBuf) < need {
			pj.padBuf = make([]byte, need)
		}
		padded := pj.padBuf[:need]
		clear(padded[copy(padded, buf):])
		buf = padded
	}
	if needCopy && boundKnown {
		// One pass: _parse_string copies a YMM word and looks for the closing quote
		// and the next backslash in the same step, so a string that has to be copied
		// anyway never needs the separate scan. This is what the reference simdjson
		// does - its copy_and_find - and the only reason the scan came first here was
		// to learn the length before reserving room for it.
		//
		// The bound serves instead: the decoded form is never longer than the source,
		// which cannot reach past the next structural character.
		start := pj.reserveStrings(maxStringSize+32, idx)
		if !parseStringSimd(buf, &pj.Strings.B) {
			return false
		}
		pj.write_tape(uint64(STRINGBUFBIT+start), '"')
		pj.Tape = append(pj.Tape, uint64(len(pj.Strings.B)-start))
		return true
	}

	srcLength := uint64(0)
	if !parseStringSimdValidateOnly(buf, &maxStringSize, &srcLength, &size) {
		return false
	}
	// Every escape sequence decodes to fewer bytes than it occupies, so the two
	// lengths agree exactly when the string holds no escapes at all.
	hasEscapes := srcLength != size

	switch {
	case !needCopy && !hasEscapes:
		// The message itself already holds the decoded form.
		pj.write_tape(idx+1, '"')

	case !hasEscapes:
		// Nothing to decode, so skip the second pass over the string: it would
		// only copy these bytes across one YMM word at a time, and the scan above
		// has already established both the length and the validity.
		start := pj.reserveStrings(size, idx)
		// Capacity is reserved, so this append cannot reallocate.
		pj.Strings.B = append(pj.Strings.B, buf[1:1+size]...)
		pj.write_tape(uint64(STRINGBUFBIT+start), '"')

	default:
		// Make sure we account for at least 32 bytes additional space due to
		// the overshoot of the copy loop in _parse_string.
		start := pj.reserveStrings(size+32, idx)
		if !parseStringSimd(buf, &pj.Strings.B) {
			return false
		}
		pj.write_tape(uint64(STRINGBUFBIT+start), '"')
		size = uint64(len(pj.Strings.B) - start)
	}
	// put length onto the tape
	pj.Tape = append(pj.Tape, size)
	return true
}

func addNumber(buf []byte, pj *ParsedJson) bool {
	tag, val := parseNumber(buf)
	if tag == 0 {
		return false
	}
	pj.writeTapeTagValFlags(tag, val)
	return true
}

func isValidTrueAtom(buf []byte) bool {
	if len(buf) >= 5 { // fast path when there is enough space left in the buffer
		const tv = uint32(0x0000000065757274) // "true    "
		locval := binary.LittleEndian.Uint32(buf)
		if locval == tv {
			return isNotStructuralOrWhitespace(buf[4]) == 0
		}
	}
	return false
}

func isValidFalseAtom(buf []byte) bool {
	if len(buf) >= 8 { // fast path when there is enough space left in the buffer
		const fv = uint64(0x00000065736c6166) // "false   "
		const mask5 = uint64(0x000000ffffffffff)
		error := uint64(isNotStructuralOrWhitespace(buf[5]))
		locval := binary.LittleEndian.Uint64(buf)
		error |= (locval & mask5) ^ fv
		return error == 0
	} else if len(buf) >= 6 {
		return bytes.Equal(buf[:5], []byte("false")) && isNotStructuralOrWhitespace(buf[5]) == 0
	}
	return false
}

func isValidNullAtom(buf []byte) bool {
	if len(buf) >= 5 { // fast path when there is enough space left in the buffer
		const nv = 0x000000006c6c756e             // "null    "
		locval := binary.LittleEndian.Uint32(buf) // we want to avoid unaligned 64-bit loads (undefined in C/C++)
		if locval == nv {
			return isNotStructuralOrWhitespace(buf[4]) == 0
		}
	}
	return false
}

func (pj *internalParsedJson) unifiedMachine() (ok, done bool) {
	buf := pj.Message
	const addOneForRoot = 1

	idx := ^uint64(0)   // location of the structural character in the input (buf)
	offset := uint64(0) // used to contain last element of containing_scope_offset
	var cur indexCursor // walks the structural indexes; see indexCursor

	////////////////////////////// START STATE /////////////////////////////
	if !pj.pushScope(retAddressStartConst) {
		goto fail
	}

	pj.write_tape(0, 'r') // r for root, 0 is going to get overwritten
	// the root is used, if nothing else, to capture the size of the tape

	if done, idx = cur.next(pj, idx); done {
		goto succeed
	}
continueRoot:
	switch buf[idx] {
	case '{':
		if !pj.pushScope(retAddressStartConst) {
			goto fail
		}
		pj.write_tape(0, '{')
		goto object_begin
	case '[':
		if !pj.pushScope(retAddressStartConst) {
			goto fail
		}
		pj.write_tape(0, '[')
		goto arrayBegin
	default:
		goto fail
	}

startContinue:
	// We are back at the top, read the next char and we should be done
	if done, idx = cur.next(pj, idx); done {
		goto succeed
	} else {
		// For an ndjson object, wrap up current object, start new root and check for minimum of 1 newline
		if buf[idx] != '\n' {
			goto fail
		}

		// Eat any empty lines
		for buf[idx] == '\n' {
			if done, idx = cur.next(pj, idx); done {
				goto succeed
			}
		}

		// Otherwise close current root
		offset = pj.containingScopeOffset[len(pj.containingScopeOffset)-1]

		// drop last element
		pj.containingScopeOffset = pj.containingScopeOffset[:len(pj.containingScopeOffset)-1]

		pj.annotate_previousloc(offset>>retAddressShift, pj.get_current_loc()+addOneForRoot)
		pj.write_tape(offset>>retAddressShift, 'r') // r is root

		// And open a new root
		if !pj.pushScope(retAddressStartConst) {
			goto fail
		}
		pj.write_tape(0, 'r') // r for root, 0 is going to get overwritten

		goto continueRoot
	}

	//////////////////////////////// OBJECT STATES /////////////////////////////

object_begin:
	if done, idx = cur.next(pj, idx); done {
		goto succeed
	}
	switch buf[idx] {
	case '"':
		if !pj.parseString(idx, cur.peek()) {
			goto fail
		}
		goto object_key_state
	case '}':
		goto scopeEnd // could also go to object_continue
	default:
		goto fail
	}

object_key_state:
	if done, idx = cur.next(pj, idx); done {
		goto succeed
	}
	if buf[idx] != ':' {
		goto fail
	}
	if done, idx = cur.next(pj, idx); done {
		goto succeed
	}
	switch buf[idx] {
	case '"':
		if !pj.parseString(idx, cur.peek()) {
			goto fail
		}

	case 't':
		if !isValidTrueAtom(buf[idx:]) {
			goto fail
		}
		pj.write_tape(0, 't')

	case 'f':
		if !isValidFalseAtom(buf[idx:]) {
			goto fail
		}
		pj.write_tape(0, 'f')

	case 'n':
		if !isValidNullAtom(buf[idx:]) {
			goto fail
		}
		pj.write_tape(0, 'n')

	case '-':
		if !addNumber(buf[idx:], &pj.ParsedJson) {
			goto fail
		}

	case '{':
		if !pj.pushScope(retAddressObjectConst) {
			goto fail
		}
		pj.write_tape(0, '{')
		// we have not yet encountered } so we need to come back for it
		goto object_begin

	case '[':
		if !pj.pushScope(retAddressObjectConst) {
			goto fail
		}
		pj.write_tape(0, '[')
		// we have not yet encountered } so we need to come back for it
		goto arrayBegin

	default:
		if buf[idx] >= '0' && buf[idx] <= '9' {
			if !addNumber(buf[idx:], &pj.ParsedJson) {
				goto fail
			}
			break
		}
		goto fail
	}

objectContinue:
	if done, idx = cur.next(pj, idx); done {
		goto succeed
	}
	switch buf[idx] {
	case ',':
		if done, idx = cur.next(pj, idx); done {
			goto succeed
		}
		if buf[idx] != '"' {
			goto fail
		}
		if !pj.parseString(idx, cur.peek()) {
			goto fail
		}
		goto object_key_state

	case '}':
		goto scopeEnd

	default:
		goto fail
	}

	////////////////////////////// COMMON STATE /////////////////////////////
scopeEnd:
	// write our tape location to the header scope
	offset = pj.containingScopeOffset[len(pj.containingScopeOffset)-1]
	// drop last element
	pj.containingScopeOffset = pj.containingScopeOffset[:len(pj.containingScopeOffset)-1]

	pj.write_tape(offset>>retAddressShift, buf[idx])
	pj.annotate_previousloc(offset>>retAddressShift, pj.get_current_loc())

	/* goto saved_state*/
	switch offset & ((1 << retAddressShift) - 1) {
	case retAddressArrayConst:
		goto arrayContinue
	case retAddressObjectConst:
		goto objectContinue
	default:
		goto startContinue
	}

	////////////////////////////// ARRAY STATES /////////////////////////////
arrayBegin:
	if done, idx = cur.next(pj, idx); done {
		goto succeed
	}
	if buf[idx] == ']' {
		goto scopeEnd // could also go to array_continue
	}

mainArraySwitch:
	// we call update char on all paths in, so we can peek at c on the
	// on paths that can accept a close square brace (post-, and at start)
	switch buf[idx] {
	case '"':
		if !pj.parseString(idx, cur.peek()) {
			goto fail
		}
	case 't':
		if !isValidTrueAtom(buf[idx:]) {
			goto fail
		}
		pj.write_tape(0, 't')

	case 'f':
		if !isValidFalseAtom(buf[idx:]) {
			goto fail
		}
		pj.write_tape(0, 'f')

	case 'n':
		if !isValidNullAtom(buf[idx:]) {
			goto fail
		}
		pj.write_tape(0, 'n')
		/* goto array_continue */

	case '-':
		if !addNumber(buf[idx:], &pj.ParsedJson) {
			goto fail
		}

	case '{':
		// we have not yet encountered ] so we need to come back for it
		if !pj.pushScope(retAddressArrayConst) {
			goto fail
		}
		pj.write_tape(0, '{') //  here the compilers knows what c is so this gets optimized
		goto object_begin

	case '[':
		// we have not yet encountered ] so we need to come back for it
		if !pj.pushScope(retAddressArrayConst) {
			goto fail
		}
		pj.write_tape(0, '[') // here the compilers knows what c is so this gets optimized
		goto arrayBegin

	default:
		if buf[idx] >= '0' && buf[idx] <= '9' {
			if !addNumber(buf[idx:], &pj.ParsedJson) {
				goto fail
			}
			break
		}
		goto fail
	}

arrayContinue:
	if done, idx = cur.next(pj, idx); done {
		goto succeed
	}
	switch buf[idx] {
	case ',':
		if done, idx = cur.next(pj, idx); done {
			goto succeed
		}
		goto mainArraySwitch

	case ']':
		goto scopeEnd

	default:
		goto fail
	}

	////////////////////////////// FINAL STATES /////////////////////////////
succeed:
	offset = pj.containingScopeOffset[len(pj.containingScopeOffset)-1]
	// drop last element
	pj.containingScopeOffset = pj.containingScopeOffset[:len(pj.containingScopeOffset)-1]

	// Sanity checks
	if len(pj.containingScopeOffset) != 0 {
		return false, done
	}

	pj.annotate_previousloc(offset>>retAddressShift, pj.get_current_loc()+addOneForRoot)
	pj.write_tape(offset>>retAddressShift, 'r') // r is root

	pj.isvalid = true
	return true, done

fail:
	return false, done
}

// structural chars here are
// they are { 0x7b } 0x7d : 0x3a [ 0x5b ] 0x5d , 0x2c (and NULL)
// we are also interested in the four whitespace characters
// space 0x20, linefeed 0x0a, horizontal tab 0x09 and carriage return 0x0d

// these are the chars that can follow a true/false/null or number atom
// and nothing else
var structuralOrWhitespaceNegated = [256]byte{
	0, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 1, 1, 0, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 1, 1, 1, 1, 1,

	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 0, 1, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 1, 0, 1, 1,

	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,

	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}

// return non-zero if not a structural or whitespace char
// zero otherwise
func isNotStructuralOrWhitespace(c byte) byte {
	return structuralOrWhitespaceNegated[c]
}
