//go:build !noasm && !appengine && gc

// The AVX2 body below started life as c2goasm output from the C sources of the
// reference simdjson implementation. Those sources are not in this repository any
// more, and this is no longer what c2goasm would emit: the AVX-512 variant was
// written by hand, its five constants live in fixed Z registers loaded once by
// __init_whitespace_and_structurals_avx512, and it returns both masks in the K
// registers named in common.h rather than through memory. Edit it here.
//
// Modifications copyright (c) 2026 DATA.BET, licensed under the Apache License,
// Version 2.0.

#include "common.h"

DATA LCDATA1<>+0x000(SB)/8, $0x0000000000000010
DATA LCDATA1<>+0x008(SB)/8, $0x00000902010c0800
DATA LCDATA1<>+0x010(SB)/8, $0x0000000000000010
DATA LCDATA1<>+0x018(SB)/8, $0x00000902010c0800
DATA LCDATA1<>+0x020(SB)/8, $0x0000000000000010
DATA LCDATA1<>+0x028(SB)/8, $0x00000902010c0800
DATA LCDATA1<>+0x030(SB)/8, $0x0000000000000010
DATA LCDATA1<>+0x038(SB)/8, $0x00000902010c0800
DATA LCDATA1<>+0x040(SB)/8, $0x7f7f7f7f7f7f7f7f
DATA LCDATA1<>+0x048(SB)/8, $0x7f7f7f7f7f7f7f7f
DATA LCDATA1<>+0x050(SB)/8, $0x7f7f7f7f7f7f7f7f
DATA LCDATA1<>+0x058(SB)/8, $0x7f7f7f7f7f7f7f7f
DATA LCDATA1<>+0x060(SB)/8, $0x7f7f7f7f7f7f7f7f
DATA LCDATA1<>+0x068(SB)/8, $0x7f7f7f7f7f7f7f7f
DATA LCDATA1<>+0x070(SB)/8, $0x7f7f7f7f7f7f7f7f
DATA LCDATA1<>+0x078(SB)/8, $0x7f7f7f7f7f7f7f7f
DATA LCDATA1<>+0x080(SB)/8, $0x0100010004120008
DATA LCDATA1<>+0x088(SB)/8, $0x0000010203000000
DATA LCDATA1<>+0x090(SB)/8, $0x0100010004120008
DATA LCDATA1<>+0x098(SB)/8, $0x0000010203000000
DATA LCDATA1<>+0x0a0(SB)/8, $0x0100010004120008
DATA LCDATA1<>+0x0a8(SB)/8, $0x0000010203000000
DATA LCDATA1<>+0x0b0(SB)/8, $0x0100010004120008
DATA LCDATA1<>+0x0b8(SB)/8, $0x0000010203000000
DATA LCDATA1<>+0x0c0(SB)/8, $0x0707070707070707
DATA LCDATA1<>+0x0c8(SB)/8, $0x0707070707070707
DATA LCDATA1<>+0x0d0(SB)/8, $0x0707070707070707
DATA LCDATA1<>+0x0d8(SB)/8, $0x0707070707070707
DATA LCDATA1<>+0x0e0(SB)/8, $0x0707070707070707
DATA LCDATA1<>+0x0e8(SB)/8, $0x0707070707070707
DATA LCDATA1<>+0x0f0(SB)/8, $0x0707070707070707
DATA LCDATA1<>+0x0f8(SB)/8, $0x0707070707070707
DATA LCDATA1<>+0x100(SB)/8, $0x1818181818181818
DATA LCDATA1<>+0x108(SB)/8, $0x1818181818181818
DATA LCDATA1<>+0x110(SB)/8, $0x1818181818181818
DATA LCDATA1<>+0x118(SB)/8, $0x1818181818181818
DATA LCDATA1<>+0x120(SB)/8, $0x1818181818181818
DATA LCDATA1<>+0x128(SB)/8, $0x1818181818181818
DATA LCDATA1<>+0x130(SB)/8, $0x1818181818181818
DATA LCDATA1<>+0x138(SB)/8, $0x1818181818181818
GLOBL LCDATA1<>(SB), 8, $320

TEXT ·_find_whitespace_and_structurals(SB), $0-24

	MOVQ input+0(FP), DI

	VMOVDQU (DI), Y8     // load low 32-bytes
	VMOVDQU 0x20(DI), Y9 // load high 32-bytes

	CALL ·__init_whitespace_and_structurals_avx2(SB)
	CALL ·__find_whitespace_and_structurals(SB)

	// The pointers are re-read rather than held across the call: the routine uses
	// CX and DX as scratch, and only the two mask registers come back.
	MOVQ whitespace+8(FP), DX
	MOVQ structurals+16(FP), CX
	MOVQ AVX2_WHITESPACE, (DX)
	MOVQ AVX2_STRUCTURALS, (CX)

	VZEROUPPER
	RET

// The five constants and the zero register, plus the two registers the masks come back
// in. Those two are the ones __finalize_structurals wants them in, so the fused loop in
// find_structural_bits_amd64.s hands them straight over instead of writing them to the
// caller's memory and reading them back. See the register map over there.

TEXT ·__init_whitespace_and_structurals_avx2(SB), $0
	LEAQ    LCDATA1<>(SB), R8
	VMOVDQU 0x000(R8), WSAS_LOWTBL_AVX2
	VMOVDQU 0x040(R8), WSAS_NIBMASK_AVX2
	VMOVDQU 0x080(R8), WSAS_HIGHTBL_AVX2
	VMOVDQU 0x0c0(R8), WSAS_STRUCT_AVX2
	VMOVDQU 0x100(R8), WSAS_WS_AVX2
	VPXOR   WSAS_ZERO_AVX2, WSAS_ZERO_AVX2, WSAS_ZERO_AVX2
	RET

// Y0 and Y1 come out holding the AND of the two table lookups, for the low and the high
// half of the block. Both tests below are made against those, so neither half is looked
// up twice.
TEXT ·__find_whitespace_and_structurals(SB), $0
	VPSHUFB Y8, WSAS_LOWTBL_AVX2, Y0  // vpshufb    ymm3, ymm2, ymm0
	VPSRLD  $4, Y8, Y2                // vpsrld    ymm0, ymm0, 4
	VPAND   WSAS_NIBMASK_AVX2, Y2, Y2 // vpand    ymm0, ymm0, ymm4
	VPSHUFB Y2, WSAS_HIGHTBL_AVX2, Y2 // vpshufb    ymm0, ymm5, ymm0
	VPAND   Y2, Y0, Y0                // vpand    ymm0, ymm0, ymm3
	VPSHUFB Y9, WSAS_LOWTBL_AVX2, Y1  // vpshufb    ymm2, ymm2, ymm1
	VPSRLD  $4, Y9, Y3                // vpsrld    ymm1, ymm1, 4
	VPAND   WSAS_NIBMASK_AVX2, Y3, Y3 // vpand    ymm1, ymm1, ymm4
	VPSHUFB Y3, WSAS_HIGHTBL_AVX2, Y3 // vpshufb    ymm1, ymm5, ymm1
	VPAND   Y3, Y1, Y1                // vpand    ymm1, ymm1, ymm2

	VPAND     WSAS_STRUCT_AVX2, Y0, Y2 // vpand    ymm3, ymm0, ymm2
	VPCMPEQB  WSAS_ZERO_AVX2, Y2, Y2   // vpcmpeqb    ymm3, ymm3, ymm4
	VPAND     WSAS_STRUCT_AVX2, Y1, Y3 // vpand    ymm2, ymm1, ymm2
	VPCMPEQB  WSAS_ZERO_AVX2, Y3, Y3   // vpcmpeqb    ymm2, ymm2, ymm4
	VPMOVMSKB Y2, AX                   // vpmovmskb    eax, ymm3
	VPMOVMSKB Y3, CX                   // vpmovmskb    esi, ymm2
	SHLQ      $32, CX                  // shl    rsi, 32
	ORQ       AX, CX                   // or    rsi, rax
	NOTQ      CX                       // not    rsi
	MOVQ      CX, AVX2_STRUCTURALS

	VPAND     WSAS_WS_AVX2, Y0, Y2   // vpand    ymm0, ymm0, ymm2
	VPCMPEQB  WSAS_ZERO_AVX2, Y2, Y2 // vpcmpeqb    ymm0, ymm0, ymm4
	VPAND     WSAS_WS_AVX2, Y1, Y3   // vpand    ymm1, ymm1, ymm2
	VPCMPEQB  WSAS_ZERO_AVX2, Y3, Y3 // vpcmpeqb    ymm1, ymm1, ymm4
	VPMOVMSKB Y2, AX                 // vpmovmskb    eax, ymm0
	VPMOVMSKB Y3, CX                 // vpmovmskb    ecx, ymm1
	SHLQ      $32, CX                // shl    rcx, 32
	ORQ       AX, CX                 // or    rcx, rax
	NOTQ      CX                     // not    rcx
	MOVQ      CX, AVX2_WHITESPACE
	RET

TEXT ·_find_whitespace_and_structurals_avx512(SB), $0-24

	MOVQ input+0(FP), DI

	VMOVDQU32 (DI), Z8

	CALL ·__init_whitespace_and_structurals_avx512(SB)
	CALL ·__find_whitespace_and_structurals_avx512(SB)

	VZEROUPPER
	KMOVQ K_WHITESPACE, whitespace+8(FP)
	KMOVQ K_STRUCTURALS, structurals+16(FP)
	RET

#define ZERO_CONST   Z20
#define WSAS_CONST_1 Z21
#define WSAS_CONST_2 Z22
#define WSAS_CONST_3 Z23
#define WSAS_CONST_4 Z24
#define WSAS_CONST_5 Z25

TEXT ·__init_whitespace_and_structurals_avx512(SB), $0
	LEAQ      LCDATA1<>(SB), R8
	VPXORD    ZERO_CONST, ZERO_CONST, ZERO_CONST
	VMOVDQU32 0x000(R8), WSAS_CONST_1
	VMOVDQU32 0x040(R8), WSAS_CONST_2
	VMOVDQU32 0x080(R8), WSAS_CONST_3
	VMOVDQU32 0x0c0(R8), WSAS_CONST_4
	VMOVDQU32 0x100(R8), WSAS_CONST_5
	RET

TEXT ·__find_whitespace_and_structurals_avx512(SB), $0
	VPSHUFB  Z8, WSAS_CONST_1, Z3
	VPSRLD   $4, Z8, Z0
	VPANDD   WSAS_CONST_2, Z0, Z0
	VPSHUFB  Z0, WSAS_CONST_3, Z0
	VPANDD   Z3, Z0, Z0
	VPANDD   WSAS_CONST_4, Z0, Z3
	VPCMPEQB ZERO_CONST, Z3, K_STRUCTURALS
	KNOTQ    K_STRUCTURALS, K_STRUCTURALS
	VPANDD   WSAS_CONST_5, Z0, Z0
	VPCMPEQB ZERO_CONST, Z0, K_WHITESPACE
	KNOTQ    K_WHITESPACE, K_WHITESPACE
	RET
