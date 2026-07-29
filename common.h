// Common defines for the amd64 assembly.
//
// Modifications copyright (c) 2026 DATA.BET, licensed under the Apache License,
// Version 2.0.

// AVX-512 mask registers. The steps of stage 1 hand their masks to each other in
// these rather than through the caller's memory.
#define K_TEMP1       K1
#define K_TEMP2       K2
#define K_ERRORMASK   K4
#define K_STRUCTURALS K5
#define K_QUOTEBITS   K6
#define K_WHITESPACE  K7

// The AVX2 equivalents. The full register map, constants included, is at the top of
// find_structural_bits_amd64.s; these three are here because the step that produces
// each is in a different file from the step that consumes it.
//
// AVX2_WHITESPACE and AVX2_STRUCTURALS are the registers __finalize_structurals
// already expects, so nothing has to move them.
#define AVX2_QUOTEBITS   R13
#define AVX2_WHITESPACE  SI
#define AVX2_STRUCTURALS DI

// The constants, loaded once per slice by the three __init_*_avx2 routines.
#define OBSS_CONST_AVX2   Y4  // '\\'
#define QMAB_CONST1_AVX2  Y5  // '"'
#define QMAB_CONST2_AVX2  Y6  // 0x80, to bias a byte into signed range
#define QMAB_CONST3_AVX2  Y7  // 0xa0, which biased is 0x20
#define WSAS_LOWTBL_AVX2  Y10
#define WSAS_NIBMASK_AVX2 Y11 // 0x7f, so VPSHUFB reads the low nibble and not the sign
#define WSAS_HIGHTBL_AVX2 Y12
#define WSAS_STRUCT_AVX2  Y13 // 0x07
#define WSAS_WS_AVX2      Y14 // 0x18
#define WSAS_ZERO_AVX2    Y15
