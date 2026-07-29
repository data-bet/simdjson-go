//go:build !noasm && !appengine && gc

// Modifications copyright (c) 2026 DATA.BET, licensed under the Apache License,
// Version 2.0.

#define MASK    AX
#define INDEX   BX
#define ZEROS   CX
#define CARRIED DX
#define PREVP   R8
#define COUNT   R9
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
//
// Neither POSITION nor CARRIED is accumulated in the loop. Every increment cancels
// against the next, so the run as a whole only moves POSITION from the previous
// structural character to the last one in this block, and the last one is wherever
// the highest set bit is; both come straight out of the mask. The previous position
// is then stepped on by the increment just written rather than reloaded from the
// trailing-zero count, which keeps the same value in one register instead of two.
TEXT ·__flatten_bits_incremental(SB), $0
	TESTQ MASK, MASK
	JE    empty

	LZCNTQ MASK, ZEROS // 63 minus the position of the last one

	// POSITION advances by (last - previous), and the previous one sits at
	// -(CARRIED+1), so the whole distance is 64 - LZCNT + CARRIED.
	ADDQ CARRIED, POSITION
	ADDQ $64, POSITION
	SUBQ ZEROS, POSITION

	MOVQ CARRIED, PREVP // PREVP = -(CARRIED+1)
	NOTQ PREVP
	MOVQ ZEROS, CARRIED // what the next block has to carry

loop:
	TZCNTQ MASK, ZEROS
	BLSRQ  MASK, MASK // clear the lowest set bit
	SUBQ   PREVP, ZEROS
	MOVL   ZEROS, (DI)(INDEX*4)
	ADDQ   ZEROS, PREVP
	ADDQ   $1, INDEX
	TESTQ  MASK, MASK
	JNE    loop
	RET

empty:
	ADDQ $64, CARRIED // no structural characters here, carry the whole block over
	RET
