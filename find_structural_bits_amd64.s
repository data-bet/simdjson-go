//go:build !noasm && !appengine && gc

// Hand-written, unlike the five steps it calls: this is the glue that runs them over
// one 64-byte block and, in _find_structural_bits_in_slice, over a whole slice of the
// message.
//
// The AVX2 register map, which the three steps in the other files share and which is
// why they cannot be read on their own:
//
//	Y8, Y9  the 64 bytes being classified
//	Y0-Y3   scratch, free across any call
//	Y4      '\\'                          __init_odd_backslash_sequences_avx2
//	Y5-Y7   '"', 0x80, 0xa0               __init_quote_mask_and_bits_avx2
//	Y10-Y15 the nibble tables, 0x7f, 0x07, 0x18, zero
//	                                      __init_whitespace_and_structurals_avx2
//
// The constants are loaded once by the three __init_ routines and then live in those
// registers for the whole slice, which is what the AVX-512 path already did with its Z
// registers. Before that each step re-loaded its own, nine VMOVDQAs and three LEAQs per
// 64 bytes.
//
// The masks also move between the steps in registers - DI, SI and R13, named
// AVX2_STRUCTURALS, AVX2_WHITESPACE and AVX2_QUOTEBITS where they are set - rather than through the
// caller's memory. The argument list is still a list of pointers because the state
// carried from one block to the next genuinely lives in the caller's memory; only the
// masks produced and consumed within a block are registers now.
//
// Modifications copyright (c) 2026 DATA.BET, licensed under the Apache License,
// Version 2.0.

#include "common.h"

TEXT ·_find_structural_bits(SB), $0-72

	MOVQ p1+0(FP), DI
	MOVQ p3+8(FP), DX

	VMOVDQU (DI), Y8     // load low 32-bytes
	VMOVDQU 0x20(DI), Y9 // load high 32-bytes

	CALL ·__init_odd_backslash_sequences_avx2(SB)
	CALL ·__init_quote_mask_and_bits_avx2(SB)
	CALL ·__init_whitespace_and_structurals_avx2(SB)

	CALL ·__find_odd_backslash_sequences(SB)

	MOVQ AX, DX                            // odd_ends + 16
	MOVQ prev_iter_inside_quote+16(FP), CX
	MOVQ error_mask+32(FP), R9

	CALL ·__find_quote_mask_and_bits(SB) // AX = quote_mask, AVX2_QUOTEBITS = quote_bits
	MOVQ AX, R12                         // quote_mask, over the next call

	CALL ·__find_whitespace_and_structurals(SB) // AVX2_STRUCTURALS, AVX2_WHITESPACE

	MOVQ quote_bits+24(FP), CX; MOVQ AVX2_QUOTEBITS, (CX)   // the caller still wants these
	MOVQ whitespace+40(FP), CX; MOVQ AVX2_WHITESPACE, (CX)   // two written out
	MOVQ structurals_in+48(FP), CX; MOVQ AVX2_STRUCTURALS, (CX)

	MOVQ R12, DX                               // DX = quote_mask
	MOVQ AVX2_QUOTEBITS, CX                        // CX = quote_bits
	MOVQ prev_iter_ends_pseudo_pred+56(FP), R8 // R8 = &prev_iter_ends_pseudo_pred

	CALL ·__finalize_structurals(SB)
	MOVQ AX, structurals+64(FP)

	VZEROUPPER
	RET

// Y0 to Y3 rather than Y10 to Y13: those now hold constants that have to survive the
// whole slice, and this runs between two iterations of the loop.
#define MASK_WHITESPACE(MAX, Y) \
	LEAQ     MASKTABLE<>(SB), DX \
	MOVQ     $MAX, BX            \
	SUBQ     CX, BX              \
	VMOVDQU  (DX)(BX*1), Y0      \ // Load mask
	VPCMPEQB Y1, Y1, Y1          \ // Set all bits
	VPXOR    Y1, Y0, Y2          \ // Invert mask
	VPAND    Y3, Y2, Y2          \ // Mask whitespace
	VPAND    Y0, Y, Y            \ // Mask message
	VPOR     Y2, Y, Y            // Combine together

TEXT ·_find_structural_bits_in_slice(SB), $0-128
	CALL ·__init_odd_backslash_sequences_avx2(SB)
	CALL ·__init_quote_mask_and_bits_avx2(SB)
	CALL ·__init_whitespace_and_structurals_avx2(SB)

	XORQ AX, AX
	MOVQ len+8(FP), CX
	ANDQ $0xffffffffffffffc0, CX
	CMPQ AX, CX
	JEQ  check_partial_load

loop:
	MOVQ    buf+0(FP), DI
	VMOVDQU (DI)(AX*1), Y8     // load low 32-bytes
	VMOVDQU 0x20(DI)(AX*1), Y9 // load high 32-bytes
	ADDQ    $0x40, AX

loop_after_load:
	PUSHQ CX
	PUSHQ AX

	MOVQ p3+16(FP), DX
	CALL ·__find_odd_backslash_sequences(SB)

	MOVQ AX, DX                            // odd_ends + 16
	MOVQ prev_iter_inside_quote+24(FP), CX
	MOVQ error_mask+40(FP), R9

	CALL ·__find_quote_mask_and_bits(SB) // AX = quote_mask, AVX2_QUOTEBITS = quote_bits
	MOVQ AX, R12                         // quote_mask, over the next two calls

	CALL ·__find_whitespace_and_structurals(SB) // AVX2_STRUCTURALS, AVX2_WHITESPACE

	MOVQ R12, DX                               // DX = quote_mask
	MOVQ AVX2_QUOTEBITS, CX                        // CX = quote_bits
	MOVQ prev_iter_ends_pseudo_pred+64(FP), R8 // R8 = &prev_iter_ends_pseudo_pred

	CALL ·__finalize_structurals(SB)

	MOVQ R12, DX                        // DX = quote_mask
	CMPQ ndjson+112(FP), $0
	JZ   skip_ndjson_detection
	CALL ·__find_newline_delimiters(SB)
	ORQ  BX, AX

skip_ndjson_detection:
	MOVQ indexes+72(FP), DI
	MOVQ index+80(FP), SI; MOVQ (SI), BX        // BX = index
	MOVQ carried+96(FP), R11; MOVQ (R11), DX    // DX = carried
	MOVQ position+104(FP), R12; MOVQ (R12), R10 // R10 = position
	CALL ·__flatten_bits_incremental(SB)
	MOVQ BX, (SI)                               // *index = BX
	MOVQ DX, (R11)                              // *carried = DX
	MOVQ R10, (R12)                             // *position = R10

	POPQ AX
	POPQ CX

	CMPQ BX, indexes_len+88(FP)
	JGE  done

	CMPQ AX, CX
	JLT  loop

	// Check if AX is not aligned on a 64-byte boundary, this signals the last (partial) iteration
	MOVQ AX, BX
	ANDQ $0x3f, BX
	CMPQ BX, $0
	JNE  done

check_partial_load:
	MOVQ len+8(FP), CX
	ANDQ $0x3f, CX
	CMPQ CX, $0
	JNE  masking       // end of message is not aligned on 64-byte boundary, so mask the remaining bytes

done:
	MOVQ AX, processed+120(FP)
	VZEROUPPER
	RET

masking:
	// Do a partial load and mask out bytes after the end of the message with whitespace
	VPBROADCASTQ WHITESPACE<>(SB), Y3 // Load padding whitespace constant

	MOVQ    buf+0(FP), DI
	VMOVDQU (DI)(AX*1), Y8 // Always load low 32-bytes
	CMPQ    CX, $0x20
	JGE     masking_high

	// Perform masking on low 32-bytes
	MASK_WHITESPACE(0x1f, Y8)
	VMOVDQU Y3, Y9
	JMP     masking_done

masking_high:
	// Perform masking on high 32-bytes
	VMOVDQU 0x20(DI)(AX*1), Y9 // Load high 32-bytes
	MASK_WHITESPACE(0x3f, Y9)

masking_done:
	ADDQ CX, AX
	JMP  loop_after_load // Rejoin loop after regular loading

DATA MASKTABLE<>+0x000(SB)/8, $0xffffffffffffffff
DATA MASKTABLE<>+0x008(SB)/8, $0xffffffffffffffff
DATA MASKTABLE<>+0x010(SB)/8, $0xffffffffffffffff
DATA MASKTABLE<>+0x018(SB)/8, $0x00ffffffffffffff
DATA MASKTABLE<>+0x020(SB)/8, $0x0000000000000000
DATA MASKTABLE<>+0x028(SB)/8, $0x0000000000000000
DATA MASKTABLE<>+0x030(SB)/8, $0x0000000000000000
DATA MASKTABLE<>+0x038(SB)/8, $0x0000000000000000
GLOBL MASKTABLE<>(SB), 8, $64

DATA WHITESPACE<>+0x000(SB)/8, $0x2020202020202020
GLOBL WHITESPACE<>(SB), 8, $8
