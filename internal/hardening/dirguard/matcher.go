package dirguard

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const MaxPolicies = 32
const MaxStates = 512
const MaxRootPath = 1024
const MaxPatterns = 16

type Matcher struct {
	transitions [][256]uint16
	accepting   []uint8
}
type byteSet [4]uint64
type edge struct {
	chars byteSet
	to    int
}
type nfaState struct {
	edges  []edge
	eps    []int
	accept bool
}
type builder struct{ states []nfaState }

func (b *builder) state() int     { b.states = append(b.states, nfaState{}); return len(b.states) - 1 }
func charSet(c byte) byteSet      { var s byteSet; s[c/64] |= uint64(1) << (c % 64); return s }
func (s *byteSet) add(c byte)     { s[c/64] |= uint64(1) << (c % 64) }
func (s byteSet) has(c byte) bool { return s[c/64]&(uint64(1)<<(c%64)) != 0 }
func anySet(slash bool) byteSet {
	var s byteSet
	for n := 1; n < 256; n++ {
		if slash || n != '/' {
			s.add(byte(n))
		}
	}
	return s
}
func (b *builder) step(from int, s byteSet) int {
	to := b.state()
	b.states[from].edges = append(b.states[from].edges, edge{s, to})
	return to
}
func (b *builder) star(from int, slashes bool) int {
	to := b.state()
	b.states[from].eps = append(b.states[from].eps, to)
	b.states[from].edges = append(b.states[from].edges, edge{anySet(slashes), from})
	return to
}
func (b *builder) directoryPrefix(from int) int {
	to, loop := b.state(), b.state()
	b.states[from].eps = append(b.states[from].eps, to, loop)
	b.states[loop].edges = append(b.states[loop].edges, edge{anySet(true), loop}, edge{charSet('/'), to})
	return to
}
func (b *builder) rune(from int) int {
	end := b.state()
	var ascii byteSet
	for n := 1; n < 128; n++ {
		if n != '/' {
			ascii.add(byte(n))
		}
	}
	b.states[from].edges = append(b.states[from].edges, edge{ascii, end})
	for size := 2; size <= 4; size++ {
		var first byteSet
		low, high := 0xc2, 0xdf
		if size == 3 {
			low, high = 0xe0, 0xef
		}
		if size == 4 {
			low, high = 0xf0, 0xf4
		}
		for n := low; n <= high; n++ {
			first.add(byte(n))
		}
		pos := b.step(from, first)
		for part := 1; part < size; part++ {
			var cont byteSet
			for n := 0x80; n <= 0xbf; n++ {
				cont.add(byte(n))
			}
			if part == size-1 {
				b.states[pos].edges = append(b.states[pos].edges, edge{cont, end})
			} else {
				pos = b.step(pos, cont)
			}
		}
	}
	return end
}
func (b *builder) pattern(pattern string) error {
	pos := b.state()
	b.states[0].eps = append(b.states[0].eps, pos)
	if !strings.Contains(pattern, "/") {
		pos = b.directoryPrefix(pos)
	}
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '\\':
			i++
			if i == len(pattern) {
				return errors.New("通配符结尾不能是转义符")
			}
			pos = b.step(pos, charSet(pattern[i]))
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if (i > 0 && pattern[i-1] != '/') || (i+2 < len(pattern) && pattern[i+2] != '/') {
					return errors.New("** 必须独立占一个路径段")
				}
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					pos = b.directoryPrefix(pos)
					i++
				} else {
					pos = b.star(pos, true)
				}
			} else {
				pos = b.star(pos, false)
			}
		case '/':
			if i+3 == len(pattern) && pattern[i+1:] == "**" {
				end := b.state()
				b.states[pos].eps = append(b.states[pos].eps, end)
				slash := b.step(pos, charSet('/'))
				tail := b.star(slash, true)
				b.states[tail].eps = append(b.states[tail].eps, end)
				pos = end
				i = len(pattern)
			} else {
				pos = b.step(pos, charSet('/'))
			}
		case '?':
			pos = b.rune(pos)
		case '[':
			var set byteSet
			j := i + 1
			negative := false
			if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
				negative = true
				j++
			}
			start := j
			for j < len(pattern) && pattern[j] != ']' {
				if pattern[j] >= 128 {
					return errors.New("字符组 [] 仅支持 ASCII 字符，可直接填写中文名称")
				}
				lo, hi := pattern[j], pattern[j]
				if j+2 < len(pattern) && pattern[j+1] == '-' && pattern[j+2] != ']' {
					hi = pattern[j+2]
					j += 2
					if hi < lo || hi >= 128 {
						return errors.New("字符组范围无效")
					}
				}
				for n := int(lo); n <= int(hi); n++ {
					set.add(byte(n))
				}
				j++
			}
			if j == len(pattern) || j == start {
				return errors.New("字符组 [] 无效")
			}
			if negative {
				for n := 1; n < 128; n++ {
					if n != '/' {
						if set.has(byte(n)) {
							set[n/64] &^= uint64(1) << uint(n%64)
						} else {
							set.add(byte(n))
						}
					}
				}
			}
			set['/'/64] &^= uint64(1) << ('/' % 64)
			pos = b.step(pos, set)
			i = j
		default:
			pos = b.step(pos, charSet(c))
		}
	}
	// 匹配到排除目录后，其所有后代都排除。
	b.states[pos].accept = true
	descendant := b.step(pos, charSet('/'))
	tail := b.star(descendant, true)
	b.states[tail].accept = true
	return nil
}

func Compile(patterns []string) (*Matcher, error) {
	if len(patterns) > MaxPatterns {
		return nil, fmt.Errorf("每条规则最多 %d 个排除模式", MaxPatterns)
	}
	b := builder{}
	b.state()
	for _, p := range patterns {
		if p == "" || len(p) > 255 || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\x00\n\r") || !utf8.ValidString(p) {
			return nil, errors.New("排除模式须为 1 至 255 字节的相对路径")
		}
		for _, segment := range strings.Split(p, "/") {
			if segment == ".." || segment == "." || segment == "" {
				return nil, errors.New("排除模式不能含空路径段、. 或 ..")
			}
		}
		if err := b.pattern(p); err != nil {
			return nil, err
		}
	}
	closure := func(seed []int) []uint64 {
		bits := make([]uint64, (len(b.states)+63)/64)
		stack := append([]int{}, seed...)
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if bits[n/64]&(uint64(1)<<uint(n%64)) != 0 {
				continue
			}
			bits[n/64] |= uint64(1) << uint(n%64)
			stack = append(stack, b.states[n].eps...)
		}
		return bits
	}
	key := func(bits []uint64) string {
		data := make([]byte, len(bits)*8)
		for i, v := range bits {
			binary.LittleEndian.PutUint64(data[i*8:], v)
		}
		return string(data)
	}
	start := closure([]int{0})
	sets := [][]uint64{start}
	known := map[string]uint16{key(start): 0}
	m := &Matcher{}
	// 等价字节共用转换，控制编译复杂度；内核使用展开后的常量时间查表。
	classes := map[string][]byte{}
	for c := 0; c < 256; c++ {
		var sig strings.Builder
		for n, s := range b.states {
			for ei, e := range s.edges {
				if e.chars.has(byte(c)) {
					fmt.Fprintf(&sig, "%d:%d,", n, ei)
				}
			}
		}
		classes[sig.String()] = append(classes[sig.String()], byte(c))
	}
	for index := 0; index < len(sets); index++ {
		if len(sets) > MaxStates {
			return nil, fmt.Errorf("排除模式过于复杂，最多 %d 个匹配状态", MaxStates)
		}
		bits := sets[index]
		var row [256]uint16
		accepted := uint8(0)
		for n, s := range b.states {
			if bits[n/64]&(uint64(1)<<uint(n%64)) != 0 && s.accept {
				accepted = 1
			}
		}
		for _, chars := range classes {
			c := chars[0]
			var seeds []int
			for n, s := range b.states {
				if bits[n/64]&(uint64(1)<<uint(n%64)) == 0 {
					continue
				}
				for _, e := range s.edges {
					if e.chars.has(c) {
						seeds = append(seeds, e.to)
					}
				}
			}
			next := closure(seeds)
			k := key(next)
			id, ok := known[k]
			if !ok {
				id = uint16(len(sets))
				known[k] = id
				sets = append(sets, next)
			}
			for _, v := range chars {
				row[v] = id
			}
		}
		m.transitions = append(m.transitions, row)
		m.accepting = append(m.accepting, accepted)
	}
	return m, nil
}
func Validate(patterns []string) error { _, err := Compile(patterns); return err }
func (m *Matcher) Match(relative string) bool {
	if relative == "" || relative == "." {
		return false
	}
	var state uint16
	for i := 0; i < len(relative); i++ {
		state = m.transitions[state][relative[i]]
	}
	return m.accepting[state] != 0
}
