// Code generated by "stringer -type Compression -trimprefix Compression"; DO NOT EDIT.

package stmtlogger

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[CompressionNone-0]
	_ = x[CompressionZSTD-1]
	_ = x[CompressionGZIP-2]
}

const _Compression_name = "NoneZSTDGZIP"

var _Compression_index = [...]uint8{0, 4, 8, 12}

func (i Compression) String() string {
	if i < 0 || i >= Compression(len(_Compression_index)-1) {
		return "Compression(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Compression_name[_Compression_index[i]:_Compression_index[i+1]]
}
