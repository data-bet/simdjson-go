//go:build !arm64 || noasm || appengine || !gc
// +build !arm64 noasm appengine !gc

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

// classifyBlock has no vector implementation on this platform, so it is the table
// lookup per byte. On amd64 this is reached only from the differential tests, since
// the whole of stage 1 is assembly there.
func classifyBlock(b *[64]byte) (backslash, quote, whitespace, structural, control, newline uint64) {
	return classifyBlockGeneric(b)
}
