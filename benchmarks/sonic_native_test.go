//go:build (amd64 && go1.17 && !go1.28) || (arm64 && go1.20 && !go1.28)

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

// The constraint above is copied verbatim from sonic.go in github.com/bytedance/sonic.
// Outside it the package builds from compat.go instead, where every entry point
// forwards to encoding/json - so a sonic row would quietly report encoding/json's
// numbers under sonic's name. sonic exports nothing to ask at run time, so the
// question is answered the same way sonic answers it, and sonic_compat_test.go holds
// the complementary constraint.
const (
	sonicIsNative  = true
	sonicNotNative = ""
)
