package main

import "bytes"

func forEachNALU(data []byte, fn func(nal []byte, nalType int)) {
	i := 0
	for i < len(data) {
		start := i
		if i+3 < len(data) && data[i] == 0 && data[i+1] == 0 {
			if data[i+2] == 1 {
				i += 3
			} else if i+4 < len(data) && data[i+2] == 0 && data[i+3] == 1 {
				i += 4
			} else {
				i++
				continue
			}
		} else {
			i++
			continue
		}

		j := i
		for j < len(data) {
			if j+3 < len(data) && data[j] == 0 && data[j+1] == 0 && (data[j+2] == 1 || (j+4 < len(data) && data[j+2] == 0 && data[j+3] == 1)) {
				break
			}
			j++
		}
		if i >= len(data) {
			break
		}
		nal := data[start:j]
		nalType := 0
		if i < len(data) {
			nalType = int(data[i]>>1) & 0x3f
		}
		fn(nal, nalType)
		i = j
	}
}

func segmentHasIDR(seg []byte) bool {
	found := false
	forEachNALU(seg, func(_ []byte, nalType int) {
		if nalType == 19 || nalType == 20 || nalType == 21 {
			found = true
		}
	})
	return found
}

func segmentHasIRAP(seg []byte) bool {
	found := false
	forEachNALU(seg, func(_ []byte, nalType int) {
		if nalType >= 16 && nalType <= 21 {
			found = true
		}
	})
	return found
}

func lastIRAPIndex(segments [][]byte) int {
	last := 0
	for i, s := range segments {
		if segmentHasIRAP(s) {
			last = i
		}
	}
	return last
}

func stripParameterSets(seg []byte) []byte {
	var out bytes.Buffer
	forEachNALU(seg, func(nal []byte, nalType int) {
		if nalType == 32 || nalType == 33 || nalType == 34 {
			return
		}
		out.Write(nal)
	})
	return out.Bytes()
}

func buildClipBitstream(segments [][]byte) []byte {
	if len(segments) == 0 {
		return nil
	}

	start := -1
	for i, seg := range segments {
		if segmentHasIDR(seg) {
			start = i
			break
		}
	}
	if start < 0 {
		start = 0
	}

	var out bytes.Buffer
	for i := start; i < len(segments); i++ {
		if i == start {
			out.Write(segments[i])
		} else {
			out.Write(stripParameterSets(segments[i]))
		}
	}
	return out.Bytes()
}
