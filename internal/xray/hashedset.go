package xray

import (
	"sort"
	"strconv"
	"strings"
)

// HashedSet mirrors @remnawave/hashed-set: XOR of per-string djb2Dual hashes.
type HashedSet struct {
	members  map[string]struct{}
	hashHigh uint32
	hashLow  uint32
}

func NewHashedSet(items ...string) *HashedSet {
	set := &HashedSet{members: make(map[string]struct{}, len(items))}
	for _, item := range items {
		set.Add(item)
	}
	return set
}

func (s *HashedSet) Add(value string) {
	if _, exists := s.members[value]; exists {
		return
	}
	s.members[value] = struct{}{}
	high, low := djb2Dual(value)
	s.hashHigh ^= high
	s.hashLow ^= low
}

func (s *HashedSet) Delete(value string) bool {
	if _, exists := s.members[value]; !exists {
		return false
	}
	delete(s.members, value)
	high, low := djb2Dual(value)
	s.hashHigh ^= high
	s.hashLow ^= low
	return true
}

func (s *HashedSet) Size() int {
	return len(s.members)
}

func (s *HashedSet) Hash64String() string {
	return formatJavaScriptHashWord(s.hashHigh) + formatJavaScriptHashWord(s.hashLow)
}

// JavaScript bitwise operators publish signed int32 values. The official
// @remnawave/hashed-set package formats those signed values before padStart.
func formatJavaScriptHashWord(value uint32) string {
	encoded := strconv.FormatInt(int64(int32(value)), 16)
	if len(encoded) >= 8 {
		return encoded
	}
	return strings.Repeat("0", 8-len(encoded)) + encoded
}

func djb2Dual(value string) (high, low uint32) {
	high = 5381
	low = 5387
	for i := 0; i < len(value); i++ {
		char := uint32(value[i])
		high = (high<<5 + high + char)
		low = (low<<6 + low + char*37)
	}
	return high, low
}

func hash64FromItems(items []string) string {
	sorted := append([]string(nil), items...)
	sort.Strings(sorted)
	set := NewHashedSet(sorted...)
	return set.Hash64String()
}
