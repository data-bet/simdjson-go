//go:build !noasm && !appengine && gc

// This started life as c2goasm output from the C sources of the reference simdjson
// implementation. Those sources are not in this repository any more, and there is no
// vector code left here at all - both routines are 64-bit bit logic over masks the
// earlier steps produced. The AVX-512 variant differs only in taking three of those
// masks out of the K registers named in common.h, and was written by hand. Edit it here.
//
// Modifications copyright (c) 2026 DATA.BET, licensed under the Apache License,
// Version 2.0.

#include "common.h"

TEXT ·_finalize_structurals(SB), $0-48

	MOVQ structurals_in+0(FP), DI
	MOVQ whitespace+8(FP), SI
	MOVQ quote_mask+16(FP), DX
	MOVQ quote_bits+24(FP), CX
	MOVQ prev_iter_ends_pseudo_pred+32(FP), R8

	CALL ·__finalize_structurals(SB)

	MOVQ AX, structurals+40(FP)
	RET

TEXT ·__finalize_structurals(SB), $0

	ANDNQ DI, DX, DI     // andn    rdi, rdx, rdi
	ORQ   CX, DI         // or    rdi, rcx
	MOVQ  DI, AX         // mov    rax, rdi
	ORQ   SI, AX         // or    rax, rsi
	LEAQ  (AX)(AX*1), R9 // lea    r9, [rax + rax]
	ORQ   (R8), R9       // or    r9, qword [r8]
	SHRQ  $63, AX        // shr    rax, 63
	MOVQ  AX, (R8)       // mov    qword [r8], rax
	NOTQ  SI             // not    rsi
	ANDNQ SI, DX, AX     // andn    rax, rdx, rsi
	ANDQ  R9, AX         // and    rax, r9
	ORQ   DI, AX         // or    rax, rdi
	NOTQ  CX             // not    rcx
	ORQ   DX, CX         // or    rcx, rdx
	ANDQ  CX, AX         // and    rax, rcx
	RET

TEXT ·__finalize_structurals_avx512(SB), $0

	KMOVQ K_WHITESPACE, SI
	KMOVQ K_QUOTEBITS, CX
	KMOVQ K_STRUCTURALS, DI
	ANDNQ DI, DX, DI        // andn    rdi, rdx, rdi
	ORQ   CX, DI            // or    rdi, rcx
	MOVQ  DI, AX            // mov    rax, rdi
	ORQ   SI, AX            // or    rax, rsi
	LEAQ  (AX)(AX*1), R9    // lea    r9, [rax + rax]
	ORQ   (R8), R9          // or    r9, qword [r8]
	SHRQ  $63, AX           // shr    rax, 63
	MOVQ  AX, (R8)          // mov    qword [r8], rax
	NOTQ  SI                // not    rsi
	ANDNQ SI, DX, AX        // andn    rax, rdx, rsi
	ANDQ  R9, AX            // and    rax, r9
	ORQ   DI, AX            // or    rax, rdi
	NOTQ  CX                // not    rcx
	ORQ   DX, CX            // or    rcx, rdx
	ANDQ  CX, AX            // and    rax, rcx
	RET
