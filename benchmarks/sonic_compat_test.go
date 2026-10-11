//go:build (!amd64 && !arm64) || go1.28 || !go1.17 || (arm64 && !go1.20)

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

package simdjson_benchmarks

// See sonic_native_test.go. This constraint is the complement sonic itself uses for
// compat.go, so the two files cover every build exactly once.
const (
	sonicIsNative  = false
	sonicNotNative = "sonic has no native implementation for this platform and toolchain, " +
		"and would report encoding/json's numbers under its own name"
)
