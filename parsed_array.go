/*
 * MinIO Cloud Storage, (C) 2020 MinIO, Inc.
 * Modifications copyright (c) 2026 DATA.BET
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

import (
	"errors"
	"fmt"
	"math"
)

// Array represents a JSON array.
// There are methods that allows to get full arrays if the value type is the same.
// Otherwise an iterator can be retrieved.
type Array struct {
	tape ParsedJson
	off  int
}

// Iter returns the array as an iterator.
// This can be used for parsing mixed content arrays.
// The first value is ready with a call to Advance.
// Calling after last element should have TypeNone.
func (a *Array) Iter() Iter {
	i := Iter{
		tape: a.tape,
		off:  a.off,
	}
	return i
}

// ForEach calls the provided function for every element.
func (a *Array) ForEach(fn func(i Iter)) {
	// Each call to fn copies an Iter, because fn gets its Iter by value. A copy
	// immediately after Advance reads the new fields with loads that are larger than the
	// stores. The store queue cannot forward these loads, and they wait for the cache.
	// On canada, this used approximately 30% of the time. For this reason, the loop
	// reads the next element before it calls fn. fn gets a copy from before that read.
	//
	// The read below is the same as Advance for one word that is not a NOP. Because fn
	// can write to the tape, the loop keeps the read only if fn did not change that
	// word. Advance does all other elements, and the end of the array, after fn.
	var i Iter
	a.tape.iterInto(&i)
	i.off = a.off
	if i.Advance() == TypeNone {
		return
	}
	for {
		e := i
		p := i.off + i.addNext
		tape := i.tape.Tape
		if p < len(tape) {
			v := tape[p]
			t, cur := Tag(v>>56), v&JSONVALUEMASK
			if addNext, fast := addNextOf(t, cur, p+1); fast {
				i.off, i.t, i.cur, i.addNext = p+1, t, cur, addNext
				fn(e)
				if tape[p] != v {
					i = e
					if i.Advance() == TypeNone {
						return
					}
				}
				continue
			}
		}
		fn(e)
		if i.Advance() == TypeNone {
			return
		}
	}
}

// DeleteElems calls the provided function for every element.
// If the function returns true the element is deleted in the array.
func (a *Array) DeleteElems(fn func(i Iter) bool) {
	i := a.Iter()
	for {
		t := i.Advance()
		if t == TypeNone {
			break
		}
		if fn(i) {
			startO := i.off - 1
			end := i.off + i.addNext
			skip := uint64(end - startO)
			for off := startO; off < end; off++ {
				i.tape.Tape[off] = (uint64(TagNop) << JSONTAGOFFSET) | skip
				skip--
			}
		}
	}
	return
}

// FirstType will return the type of the first element.
// If there are no elements, TypeNone is returned.
func (a *Array) FirstType() Type {
	iter := a.Iter()
	return iter.PeekNext()
}

// MarshalJSON will marshal the entire remaining scope of the iterator.
func (a *Array) MarshalJSON() ([]byte, error) {
	return a.MarshalJSONBuffer(nil)
}

// MarshalJSONBuffer will marshal all elements.
// An optional buffer can be provided for fewer allocations.
// Output will be appended to the destination.
func (a *Array) MarshalJSONBuffer(dst []byte) ([]byte, error) {
	dst = append(dst, '[')
	i := a.Iter()
	var elem Iter
	for {
		t, err := i.AdvanceIter(&elem)
		if err != nil {
			return nil, err
		}
		if t == TypeNone {
			break
		}
		dst, err = elem.MarshalJSONBuffer(dst)
		if err != nil {
			return nil, err
		}
		if i.PeekNextTag() == TagArrayEnd {
			break
		}
		dst = append(dst, ',')
	}
	if i.PeekNextTag() != TagArrayEnd {
		return nil, errors.New("expected TagArrayEnd as final tag in array")
	}
	dst = append(dst, ']')
	return dst, nil
}

// Interface returns the array as a slice of interfaces.
// See Iter.Interface() for a reference on value types.
func (a *Array) Interface() ([]interface{}, error) {
	// Estimate length. Assume one value per element.
	lenEst := (len(a.tape.Tape) - a.off - 1) / 2
	if lenEst < 0 {
		lenEst = 0
	}
	dst := make([]interface{}, 0, lenEst)
	i := a.Iter()
	for i.Advance() != TypeNone {
		elem, err := i.Interface()
		if err != nil {
			return nil, err
		}
		dst = append(dst, elem)
	}
	return dst, nil
}

// AsFloat returns the array values as float.
// Integers are automatically converted to float.
func (a *Array) AsFloat() ([]float64, error) {
	// Estimate length
	lenEst := (len(a.tape.Tape) - a.off - 1) / 2
	if lenEst < 0 {
		lenEst = 0
	}
	dst := make([]float64, 0, lenEst)

readArray:
	for {
		tag := Tag(a.tape.Tape[a.off] >> 56)
		a.off++
		switch tag {
		case TagFloat:
			if len(a.tape.Tape) <= a.off {
				return nil, errors.New("corrupt input: expected float, but no more values")
			}
			dst = append(dst, math.Float64frombits(a.tape.Tape[a.off]))
		case TagInteger:
			if len(a.tape.Tape) <= a.off {
				return nil, errors.New("corrupt input: expected integer, but no more values")
			}
			dst = append(dst, float64(int64(a.tape.Tape[a.off])))
		case TagUint:
			if len(a.tape.Tape) <= a.off {
				return nil, errors.New("corrupt input: expected integer, but no more values")
			}
			dst = append(dst, float64(a.tape.Tape[a.off]))
		case TagArrayEnd:
			break readArray
		default:
			return nil, fmt.Errorf("unable to convert type %v to float", tag)
		}
		a.off++
	}
	return dst, nil
}

// AsInteger returns the array values as int64 values.
// Uints/Floats are automatically converted to int64 if they fit within the range.
func (a *Array) AsInteger() ([]int64, error) {
	// Estimate length
	lenEst := (len(a.tape.Tape) - a.off - 1) / 2
	if lenEst < 0 {
		lenEst = 0
	}
	dst := make([]int64, 0, lenEst)
readArray:
	for {
		tag := Tag(a.tape.Tape[a.off] >> 56)
		a.off++
		switch tag {
		case TagFloat:
			if len(a.tape.Tape) <= a.off {
				return nil, errors.New("corrupt input: expected float, but no more values")
			}
			val := math.Float64frombits(a.tape.Tape[a.off])
			if val > math.MaxInt64 {
				return nil, errors.New("float value overflows int64")
			}
			if val < math.MinInt64 {
				return nil, errors.New("float value underflows int64")
			}
			dst = append(dst, int64(val))
		case TagInteger:
			if len(a.tape.Tape) <= a.off {
				return nil, errors.New("corrupt input: expected integer, but no more values")
			}
			dst = append(dst, int64(a.tape.Tape[a.off]))
		case TagUint:
			if len(a.tape.Tape) <= a.off {
				return nil, errors.New("corrupt input: expected integer, but no more values")
			}

			val := a.tape.Tape[a.off]
			if val > math.MaxInt64 {
				return nil, errors.New("unsigned integer value overflows int64")
			}
			dst = append(dst, int64(val))
		case TagArrayEnd:
			break readArray
		default:
			return nil, fmt.Errorf("unable to convert type %v to integer", tag)
		}
		a.off++
	}
	return dst, nil
}

// AsUint64 returns the array values as float.
// Uints/Floats are automatically converted to uint64 if they fit within the range.
func (a *Array) AsUint64() ([]uint64, error) {
	// Estimate length
	lenEst := (len(a.tape.Tape) - a.off - 1) / 2
	if lenEst < 0 {
		lenEst = 0
	}
	dst := make([]uint64, 0, lenEst)
readArray:
	for {
		tag := Tag(a.tape.Tape[a.off] >> 56)
		a.off++
		switch tag {
		case TagFloat:
			if len(a.tape.Tape) <= a.off {
				return nil, errors.New("corrupt input: expected float, but no more values")
			}
			val := math.Float64frombits(a.tape.Tape[a.off])
			if val > math.MaxInt64 {
				return nil, errors.New("float value overflows uint64")
			}
			if val < 0 {
				return nil, errors.New("float value is negative")
			}
			dst = append(dst, uint64(val))
		case TagInteger:
			if len(a.tape.Tape) <= a.off {
				return nil, errors.New("corrupt input: expected integer, but no more values")
			}
			val := int64(a.tape.Tape[a.off])
			if val < 0 {
				return nil, errors.New("int64 value is negative")
			}
			dst = append(dst, uint64(val))
		case TagUint:
			if len(a.tape.Tape) <= a.off {
				return nil, errors.New("corrupt input: expected integer, but no more values")
			}

			dst = append(dst, a.tape.Tape[a.off])
		case TagArrayEnd:
			break readArray
		default:
			return nil, fmt.Errorf("unable to convert type %v to integer", tag)
		}
		a.off++
	}
	return dst, nil
}

// AsString returns the array values as a slice of strings.
// No conversion is done.
func (a *Array) AsString() ([]string, error) {
	// Estimate length, each string is 2 tape entries
	lenEst := (len(a.tape.Tape) - a.off - 1) / 2
	if lenEst < 0 {
		lenEst = 0
	}
	dst := make([]string, 0, lenEst)
	i := a.Iter()
	var elem Iter
	for {
		t, err := i.AdvanceIter(&elem)
		if err != nil {
			return nil, err
		}
		switch t {
		case TypeNone:
			return dst, nil
		case TypeString:
			s, err := elem.String()
			if err != nil {
				return nil, err
			}
			dst = append(dst, s)
		default:
			return nil, fmt.Errorf("element in array is not string, but %v", t)
		}
	}
}

// AsStringCvt returns the array values as a slice of strings.
// Scalar types are converted.
// Root, Object and Arrays are not supported an will return an error if found.
func (a *Array) AsStringCvt() ([]string, error) {
	// Estimate length, each scalar is 2 tape entries
	lenEst := (len(a.tape.Tape) - a.off - 1) / 2
	if lenEst < 0 {
		lenEst = 0
	}
	dst := make([]string, 0, lenEst)
	i := a.Iter()
	var elem Iter
	for {
		t, err := i.AdvanceIter(&elem)
		if err != nil {
			return nil, err
		}
		switch t {
		case TypeNone:
			return dst, nil
		default:
			s, err := elem.StringCvt()
			if err != nil {
				return nil, err
			}
			dst = append(dst, s)
		}
	}
}
