package main

import (
	"strconv"
	"strings"
)

// This file implements a hand written lexer/parser for Paradox script files.
//
// It replaces the PEG grammar the tool used before. The grammar based parser
// aborted the whole run whenever it met syntax that was not in its rule set,
// which modern HOI4 and mod files hit constantly: comparison operators such as
// ">=" and "!=", unquoted paths containing "/", inline math in "@[ ... ]",
// bracketed loc commands and pipes inside values.
//
// The parser below never fails. Anything it does not understand is reported
// through warnf and skipped, so a single malformed line can no longer stop the
// image from being generated.

// PNode is one "key op value" entry of a Paradox script file.
// Value holds the right hand side when it is a scalar, Block when it is a
// "{ ... }" scope. Both can be set for constructs like "color = hsv{ 1 2 3 }".
type PNode struct {
	Key    string
	Op     string
	Value  string
	Quoted bool
	Block  *PBlock
	Line   int
}

// PBlock is a "{ ... }" scope. Nodes holds key/value entries, Values holds
// loose list items such as the file names in "fontfiles = { "a" "b" }".
type PBlock struct {
	Nodes  []*PNode
	Values []string
	Line   int
}

// Get returns the first child with the given key, case insensitive.
func (b *PBlock) Get(key string) *PNode {
	if b == nil {
		return nil
	}
	for _, n := range b.Nodes {
		if strings.EqualFold(n.Key, key) {
			return n
		}
	}
	return nil
}

// GetAll returns every child with the given key, case insensitive.
func (b *PBlock) GetAll(key string) []*PNode {
	if b == nil {
		return nil
	}
	var out []*PNode
	for _, n := range b.Nodes {
		if strings.EqualFold(n.Key, key) {
			out = append(out, n)
		}
	}
	return out
}

// Str returns the scalar value of a child, or "" when it is missing.
func (b *PBlock) Str(key string) string {
	if n := b.Get(key); n != nil {
		return n.Value
	}
	return ""
}

// Has reports whether a child with that key exists.
func (b *PBlock) Has(key string) bool {
	return b.Get(key) != nil
}

// Int returns the numeric value of a child. Floats are truncated, which
// matches how the game treats focus coordinates.
func (b *PBlock) Int(key string) (int, bool) {
	n := b.Get(key)
	if n == nil {
		return 0, false
	}
	return parseLooseInt(n.Value)
}

// parseLooseInt accepts ints, floats and values with stray characters so that
// a typo in one coordinate does not abort the whole tree.
func parseLooseInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if i, err := strconv.Atoi(s); err == nil {
		return i, true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int(f), true
	}
	// Strip anything that is not part of a number and retry once.
	var b strings.Builder
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9', r == '.':
			b.WriteRune(r)
		case r == '-' && i == 0:
			b.WriteRune(r)
		}
	}
	if f, err := strconv.ParseFloat(b.String(), 64); err == nil {
		return int(f), true
	}
	return 0, false
}

type tokKind int

const (
	tokEOF tokKind = iota
	tokWord
	tokString
	tokOp
	tokLBrace
	tokRBrace
)

type token struct {
	kind tokKind
	val  string
	line int
}

type lexer struct {
	src  string
	pos  int
	line int
}

func newLexer(src string) *lexer {
	return &lexer{src: src, line: 1}
}

func (l *lexer) skipSpaceAndComments() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '\n':
			l.line++
			l.pos++
		case c == ' ' || c == '\t' || c == '\r' || c == '\v' || c == '\f' || c == 0:
			l.pos++
		case c == ';':
			// Statement separator, carries no meaning for us.
			l.pos++
		case c == '#':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
		default:
			return
		}
	}
}

// isWordBreak reports whether c ends an unquoted word.
func isWordBreak(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '\v', '\f', 0, '{', '}', '=', '"', '#', '<', '>', ';':
		return true
	}
	return false
}

func (l *lexer) next() token {
	l.skipSpaceAndComments()
	if l.pos >= len(l.src) {
		return token{kind: tokEOF, line: l.line}
	}

	start := l.pos
	line := l.line
	c := l.src[l.pos]

	switch c {
	case '{':
		l.pos++
		return token{kind: tokLBrace, val: "{", line: line}
	case '}':
		l.pos++
		return token{kind: tokRBrace, val: "}", line: line}
	case '"':
		return l.lexString()
	case '=':
		l.pos++
		if l.pos < len(l.src) && l.src[l.pos] == '=' {
			l.pos++
			return token{kind: tokOp, val: "==", line: line}
		}
		return token{kind: tokOp, val: "=", line: line}
	case '<':
		l.pos++
		if l.pos < len(l.src) && (l.src[l.pos] == '=' || l.src[l.pos] == '>') {
			op := "<" + string(l.src[l.pos])
			l.pos++
			return token{kind: tokOp, val: op, line: line}
		}
		return token{kind: tokOp, val: "<", line: line}
	case '>':
		l.pos++
		if l.pos < len(l.src) && l.src[l.pos] == '=' {
			l.pos++
			return token{kind: tokOp, val: ">=", line: line}
		}
		return token{kind: tokOp, val: ">", line: line}
	case '!':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
			l.pos += 2
			return token{kind: tokOp, val: "!=", line: line}
		}
	case '?':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
			l.pos += 2
			return token{kind: tokOp, val: "?=", line: line}
		}
	}

	// Unquoted word. Brackets are consumed whole so that inline math such as
	// "@[ base_cost * 2 ]" and loc commands such as "[Root.GetName]" survive
	// as a single token even though they contain spaces.
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '[' {
			l.consumeBracket()
			continue
		}
		if c == '!' || c == '?' {
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' && l.pos > start {
				break
			}
			l.pos++
			continue
		}
		if isWordBreak(c) {
			break
		}
		l.pos++
	}

	if l.pos == start {
		// Nothing consumed: a lone character we have no rule for. Skip it so
		// the parser cannot spin forever on it.
		l.pos++
		return l.next()
	}
	return token{kind: tokWord, val: l.src[start:l.pos], line: line}
}

// consumeBracket advances past a balanced [ ... ] group.
func (l *lexer) consumeBracket() {
	depth := 0
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '\n' {
			l.line++
		}
		l.pos++
		switch c {
		case '[':
			depth++
		case ']':
			depth--
			if depth <= 0 {
				return
			}
		}
	}
}

func (l *lexer) lexString() token {
	line := l.line
	l.pos++ // opening quote
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch c {
		case '\\':
			// Keep the escape sequence intact, localisation relies on \n.
			if l.pos+1 < len(l.src) {
				b.WriteByte(c)
				b.WriteByte(l.src[l.pos+1])
				if l.src[l.pos+1] == '\n' {
					l.line++
				}
				l.pos += 2
				continue
			}
			l.pos++
		case '"':
			l.pos++
			return token{kind: tokString, val: b.String(), line: line}
		case '\n':
			// An unterminated string must not swallow the rest of the file.
			l.line++
			l.pos++
			return token{kind: tokString, val: b.String(), line: line}
		default:
			b.WriteByte(c)
			l.pos++
		}
	}
	return token{kind: tokString, val: b.String(), line: line}
}

type pdxParser struct {
	lex     *lexer
	peeked  *token
	source  string
	broken  int
	maxWarn int
}

func (p *pdxParser) next() token {
	if p.peeked != nil {
		t := *p.peeked
		p.peeked = nil
		return t
	}
	return p.lex.next()
}

func (p *pdxParser) peek() token {
	if p.peeked == nil {
		t := p.lex.next()
		p.peeked = &t
	}
	return *p.peeked
}

func (p *pdxParser) complain(line int, format string, args ...interface{}) {
	p.broken++
	if p.broken > p.maxWarn {
		return
	}
	warnf("%v:%v: "+format, append([]interface{}{p.source, line}, args...)...)
}

const maxPDXDepth = 250

// ParsePDX parses Paradox script into a tree. It always returns a block;
// syntax it cannot make sense of is logged and skipped.
func ParsePDX(src, source string) *PBlock {
	src = stripBOM(src)
	p := &pdxParser{lex: newLexer(src), source: source, maxWarn: 10}
	root := p.parseBlock(0, true)
	if p.broken > p.maxWarn {
		warnf("%v: %v more syntax problems were skipped", source, p.broken-p.maxWarn)
	}
	return root
}

func (p *pdxParser) parseBlock(depth int, top bool) *PBlock {
	blk := &PBlock{Line: p.peek().line}
	if depth > maxPDXDepth {
		p.complain(blk.Line, "nesting deeper than %v levels, rest of the scope ignored", maxPDXDepth)
		p.skipToBlockEnd()
		return blk
	}

	for {
		t := p.peek()
		switch t.kind {
		case tokEOF:
			if !top {
				p.complain(t.line, "file ended before the scope was closed")
			}
			return blk

		case tokRBrace:
			p.next()
			if top {
				// Stray closing brace at file level, ignore and keep going.
				p.complain(t.line, "unexpected \"}\"")
				continue
			}
			return blk

		case tokLBrace:
			// Anonymous scope inside a list, e.g. "{ { a b } { c d } }".
			p.next()
			sub := p.parseBlock(depth+1, false)
			blk.Nodes = append(blk.Nodes, &PNode{Block: sub, Line: t.line})

		case tokOp:
			p.next()
			p.complain(t.line, "operator %q without a left hand side", t.val)

		case tokWord, tokString:
			p.next()
			blk.Nodes, blk.Values = p.parseEntry(t, blk.Nodes, blk.Values, depth)
		}
	}
}

func (p *pdxParser) parseEntry(key token, nodes []*PNode, values []string, depth int) ([]*PNode, []string) {
	nt := p.peek()

	switch nt.kind {
	case tokOp:
		p.next()
		node := &PNode{Key: key.val, Op: nt.val, Line: key.line}
		vt := p.peek()
		switch vt.kind {
		case tokLBrace:
			p.next()
			node.Block = p.parseBlock(depth+1, false)
		case tokWord, tokString:
			p.next()
			node.Value = vt.val
			node.Quoted = vt.kind == tokString
			// "color = hsv{ 1 2 3 }" puts a scope right after the scalar.
			if p.peek().kind == tokLBrace {
				p.next()
				node.Block = p.parseBlock(depth+1, false)
			}
		default:
			p.complain(key.line, "%q = has no value", key.val)
			return nodes, values
		}
		return append(nodes, node), values

	case tokLBrace:
		// "key { ... }" with the "=" left out.
		p.next()
		node := &PNode{Key: key.val, Op: "=", Line: key.line}
		node.Block = p.parseBlock(depth+1, false)
		return append(nodes, node), values

	default:
		// A loose value inside a list.
		return nodes, append(values, key.val)
	}
}

// skipToBlockEnd consumes tokens until the current scope is closed.
func (p *pdxParser) skipToBlockEnd() {
	depth := 1
	for {
		t := p.next()
		switch t.kind {
		case tokEOF:
			return
		case tokLBrace:
			depth++
		case tokRBrace:
			depth--
			if depth <= 0 {
				return
			}
		}
	}
}
