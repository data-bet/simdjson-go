//go:build arm64 && !noasm && !appengine && gc

// Copyright (c) 2026 DATA.BET
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Written by hand, not generated from anything.
//
// This is the one step of stage 1 that needs a vector unit: turning 64 bytes into the
// six 64-bit masks the rest of it works on. Everything after it is integer arithmetic
// over those masks, which Go compiles about as well as assembly would on arm64 - it
// has CLZ and RBIT for the bit scans - so only this step is here. See
// stage1_find_marks_generic.go for the rest, which this shares with every other
// platform that has no assembly.
//
// It is the same classification the AVX2 path does, with two differences forced by
// the instruction set:
//
//   - TBL yields zero for an index of 16 or more, where VPSHUFB yields zero only for
//     an index with bit 7 set and otherwise uses the low four bits. So the low-nibble
//     index is masked to 0x0f here, and the high half of the high-nibble table is
//     zeroed instead - which is what makes a byte over 0x7f classify as nothing, the
//     rule VPSHUFB gets for free. buildJSONCharClass spells the same rule out.
//
//   - There is no VPMOVMSKB. Gathering the high bit of each of 64 bytes into a
//     uint64 takes the AND-with-weights and four pairwise adds in MOVEMASK below.

#include "textflag.h"

DATA neonTables<>+0x00(SB)/8, $0x0000000000000010 // low nibble table, from the AVX2 LCDATA1
DATA neonTables<>+0x08(SB)/8, $0x00000902010c0800
DATA neonTables<>+0x10(SB)/8, $0x0100010004120008 // high nibble table, top half zeroed
DATA neonTables<>+0x18(SB)/8, $0x0000000000000000
DATA neonTables<>+0x20(SB)/8, $0x8040201008040201 // one bit per byte lane, for MOVEMASK
DATA neonTables<>+0x28(SB)/8, $0x8040201008040201
GLOBL neonTables<>(SB), RODATA|NOPTR, $48

// MOVEMASK gathers the high bit of each byte of M0, M1, M2 and M3 - 64 lanes, each
// 0x00 or 0xff - into the low 64 bits of DST, lane 0 in bit 0.
//
// Each lane is reduced to the single bit that is its own position within its group of
// eight, and the pairwise adds then sum the groups together without ever carrying,
// since within a group the bits are distinct. Four rounds fold 64 lanes into 8 bytes.
// The parameters are not named A..D: a macro parameter D would also substitute into
// the D of V12.D[0] below, and the assembler then reports the register as a bad
// arrangement specifier rather than as a macro problem.
#define MOVEMASK(M0, M1, M2, M3, DST) \
	VAND  V18.B16, M0, V12.B16      \
	VAND  V18.B16, M1, V13.B16      \
	VAND  V18.B16, M2, V14.B16      \
	VAND  V18.B16, M3, V15.B16      \
	VADDP V13.B16, V12.B16, V12.B16 \
	VADDP V15.B16, V14.B16, V14.B16 \
	VADDP V14.B16, V12.B16, V12.B16 \
	VADDP V12.B16, V12.B16, V12.B16 \
	VMOV  V12.D[0], DST

// NIBBLES leaves in DST the AND of the two table lookups for the bytes of SRC, which
// is the value the 0x07 and 0x18 tests below are made against.
#define NIBBLES(SRC, DST)          \
	VAND  V19.B16, SRC, V8.B16     \
	VTBL  V8.B16, [V16.B16], V9.B16 \
	VUSHR $4, SRC, V10.B16         \
	VTBL  V10.B16, [V17.B16], V11.B16 \
	VAND  V11.B16, V9.B16, DST

// func classifyBlockNEON(block *[64]byte, out *[6]uint64)
TEXT ·classifyBlockNEON(SB), NOSPLIT, $0-16
	MOVD block+0(FP), R0
	MOVD out+8(FP), R1
	MOVD $neonTables<>(SB), R2

	VLD1 (R0), [V0.B16, V1.B16, V2.B16, V3.B16]
	VLD1 (R2), [V16.B16, V17.B16, V18.B16]

	VEOR  V31.B16, V31.B16, V31.B16
	VMOVI $0x0f, V19.B16
	VMOVI $0x07, V20.B16 // a structural character, per the nibble tables
	VMOVI $0x18, V21.B16 // whitespace, per the nibble tables
	VMOVI $0x22, V22.B16 // '"'
	VMOVI $0x5c, V23.B16 // '\\'
	VMOVI $0x0a, V24.B16 // '\n'

	NIBBLES(V0.B16, V4.B16)
	NIBBLES(V1.B16, V5.B16)
	NIBBLES(V2.B16, V6.B16)
	NIBBLES(V3.B16, V7.B16)

	// '\\'
	VCMEQ V23.B16, V0.B16, V8.B16
	VCMEQ V23.B16, V1.B16, V9.B16
	VCMEQ V23.B16, V2.B16, V10.B16
	VCMEQ V23.B16, V3.B16, V11.B16
	MOVEMASK(V8.B16, V9.B16, V10.B16, V11.B16, R3)
	MOVD R3, 0(R1)

	// '"'
	VCMEQ V22.B16, V0.B16, V8.B16
	VCMEQ V22.B16, V1.B16, V9.B16
	VCMEQ V22.B16, V2.B16, V10.B16
	VCMEQ V22.B16, V3.B16, V11.B16
	MOVEMASK(V8.B16, V9.B16, V10.B16, V11.B16, R3)
	MOVD R3, 8(R1)

	// whitespace: any bit of 0x18 set in the nibble product
	VCMTST V21.B16, V4.B16, V8.B16
	VCMTST V21.B16, V5.B16, V9.B16
	VCMTST V21.B16, V6.B16, V10.B16
	VCMTST V21.B16, V7.B16, V11.B16
	MOVEMASK(V8.B16, V9.B16, V10.B16, V11.B16, R3)
	MOVD R3, 16(R1)

	// structural: any bit of 0x07 set in the nibble product
	VCMTST V20.B16, V4.B16, V8.B16
	VCMTST V20.B16, V5.B16, V9.B16
	VCMTST V20.B16, V6.B16, V10.B16
	VCMTST V20.B16, V7.B16, V11.B16
	MOVEMASK(V8.B16, V9.B16, V10.B16, V11.B16, R3)
	MOVD R3, 24(R1)

	// below 0x20, which is an error inside a string. Shifting off the low five bits
	// leaves zero for exactly those bytes, so this needs no second constant.
	VUSHR $5, V0.B16, V8.B16
	VUSHR $5, V1.B16, V9.B16
	VUSHR $5, V2.B16, V10.B16
	VUSHR $5, V3.B16, V11.B16
	VCMEQ V31.B16, V8.B16, V8.B16
	VCMEQ V31.B16, V9.B16, V9.B16
	VCMEQ V31.B16, V10.B16, V10.B16
	VCMEQ V31.B16, V11.B16, V11.B16
	MOVEMASK(V8.B16, V9.B16, V10.B16, V11.B16, R3)
	MOVD R3, 32(R1)

	// '\n', for ndjson
	VCMEQ V24.B16, V0.B16, V8.B16
	VCMEQ V24.B16, V1.B16, V9.B16
	VCMEQ V24.B16, V2.B16, V10.B16
	VCMEQ V24.B16, V3.B16, V11.B16
	MOVEMASK(V8.B16, V9.B16, V10.B16, V11.B16, R3)
	MOVD R3, 40(R1)

	RET
