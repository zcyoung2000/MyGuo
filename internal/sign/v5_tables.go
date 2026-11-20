// Constants extracted from reference arm64 db618cc9faa4; metasec image sbox at 0xffcf9.
package sign

import _ "embed"

//go:embed v5_sbox.bin
var v5Sbox []byte

var v5Con = [4][4]int{{2, 0, 3, 1}, {1, 3, 2, 0}, {3, 2, 1, 0}, {2, 3, 1, 0}}
var v5Order = [4][16]int{{0, 9, 14, 15, 4, 13, 2, 7, 8, 1, 6, 3, 12, 5, 10, 11}, {0, 9, 14, 11, 4, 13, 2, 3, 8, 1, 6, 15, 12, 5, 10, 7}, {0, 9, 14, 3, 4, 13, 2, 15, 8, 1, 6, 11, 12, 5, 10, 7}, {0, 9, 14, 15, 4, 13, 2, 3, 8, 1, 6, 11, 12, 5, 10, 7}}
var v5Init = [4]uint32{0xbbb5de72, 0xd4638d5b, 0x92a5f73b, 0x89f8a1c9}
var v5Extract = [8]uint64{6, 5, 3, 4, 2, 7, 1, 0}
var v5Deposit = [8]uint64{1, 7, 3, 6, 2, 0, 4, 5}
