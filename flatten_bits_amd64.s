//go:build !noasm && !appengine && gc

// Modifications copyright (c) 2026 DATA.BET, licensed under the Apache License,
// Version 2.0.

#define MASK    AX
#define INDEX   BX
#define ZEROS   CX
#define CARRIED DX
#define PREVP   R8
#define POSITION R10

TEXT ·_flatten_bits_incremental(SB), $0-40

	MOVQ base_ptr+0(FP), DI
	MOVQ pbase+8(FP), SI
	MOVQ mask+16(FP), MASK
	MOVQ carried+24(FP), R11
	MOVQ position+32(FP), R12
	MOVQ (SI), INDEX
	MOVQ (R11), CARRIED
	MOVQ (R12), POSITION
	CALL ·__flatten_bits_incremental(SB)
	MOVQ POSITION, (R12)
	MOVQ CARRIED, (R11)
	MOVQ INDEX, (SI)
	RET

// __flatten_bits_incremental turns the structural bitmask in MASK into the
// increments the tape builder consumes, appending them at INDEX and advancing
// POSITION to the absolute offset of the last one.
//
// Each increment is the distance from the previous structural character, so the
// first one in a block also has to cover the bits carried over from blocks that
// held none. Seeding the previous bit position with -(CARRIED+1) folds that in:
// every increment is then just the difference between two bit positions.
//
// Bits are taken with BLSR rather than by shifting MASK right past each one. A
// variable shift made MASK depend on the trailing-zero count, putting TZCNT's three
// cycle latency on the loop-carried path; BLSR depends on MASK alone, so the chain
// is one cycle and TZCNT overlaps with the rest of the body.
TEXT ·__flatten_bits_incremental(SB), $0
	TESTQ MASK, MASK
	JE    empty

	MOVQ CARRIED, PREVP
	NOTQ PREVP            // PREVP = -(CARRIED+1)
	XORQ CARRIED, CARRIED // CARRIED has been folded into PREVP

loop:
	TZCNTQ MASK, ZEROS
	BLSRQ  MASK, MASK // clear the lowest set bit
	MOVQ   ZEROS, R9
	SUBQ   PREVP, R9  // increment since the previous structural character
	MOVL   R9, (DI)(INDEX*4)
	ADDQ   $1, INDEX
	ADDQ   R9, POSITION
	MOVQ   ZEROS, PREVP
	TESTQ  MASK, MASK
	JNE    loop

	// The bits above the last one found are carried into the next block.
	MOVQ $63, CARRIED
	SUBQ PREVP, CARRIED
	RET

empty:
	ADDQ $64, CARRIED // no structural characters here, carry the whole block over
	RET
