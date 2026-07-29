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

//go:noescape
func classifyBlockNEON(block *[64]byte, out *[6]uint64)

// classifyBlock on arm64 goes through NEON, which is mandatory on the architecture
// and so needs no feature test. The rest of stage 1 is the shared Go in
// stage1_find_marks_generic.go; see stage1_classify_arm64.s for why only this step is
// assembly.
func classifyBlock(b *[64]byte) (backslash, quote, whitespace, structural, control, newline uint64) {
	var out [6]uint64
	classifyBlockNEON(b, &out)
	return out[0], out[1], out[2], out[3], out[4], out[5]
}
