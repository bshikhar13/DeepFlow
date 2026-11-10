// Package expr implements the small, safe arithmetic language that
// visualization specs use. Only numbers, declared parameter names, basic
// operators and a fixed set of functions are accepted, so model output can
// define a formula but never run code. web/src/lib/expr.ts mirrors it.
package expr

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// MaxLen is the longest accepted expression, in characters.
const MaxLen = 200

const maxDepth = 32

type node func(vars map[string]float64) float64

// Expr is a parsed expression.
type Expr struct{ root node }

// Eval computes the expression. Results may be NaN or infinite.
func (e *Expr) Eval(vars map[string]float64) float64 { return e.root(vars) }

type fn struct {
	min, max int // argument count bounds; max -1 means unbounded
	call     func(a []float64) float64
}

func unary(f func(float64) float64) fn {
	return fn{1, 1, func(a []float64) float64 { return f(a[0]) }}
}

var functions = map[string]fn{
	"min": {1, -1, func(a []float64) float64 {
		m := a[0]
		for _, v := range a[1:] {
			m = math.Min(m, v)
		}
		return m
	}},
	"max": {1, -1, func(a []float64) float64 {
		m := a[0]
		for _, v := range a[1:] {
			m = math.Max(m, v)
		}
		return m
	}},
	"abs":   unary(math.Abs),
	"sqrt":  unary(math.Sqrt),
	"log":   unary(math.Log),
	"log2":  unary(math.Log2),
	"log10": unary(math.Log10),
	"exp":   unary(math.Exp),
	"floor": unary(math.Floor),
	"ceil":  unary(math.Ceil),
}

type parser struct {
	s       string
	i       int
	depth   int
	allowed map[string]bool
}

// Parse compiles src, permitting only the allowed identifiers as variables.
func Parse(src string, allowed []string) (*Expr, error) {
	if len(src) > MaxLen {
		return nil, fmt.Errorf("expression longer than %d characters", MaxLen)
	}
	p := &parser{s: src, allowed: map[string]bool{}}
	for _, a := range allowed {
		p.allowed[a] = true
	}
	n, err := p.addSub()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i != len(p.s) {
		return nil, fmt.Errorf("unexpected %q at %d", p.s[p.i:], p.i)
	}
	return &Expr{root: n}, nil
}

func (p *parser) space() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *parser) peek() byte {
	p.space()
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *parser) addSub() (node, error) {
	left, err := p.mulDiv()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek() {
		case '+':
			p.i++
			right, err := p.mulDiv()
			if err != nil {
				return nil, err
			}
			l := left
			left = func(v map[string]float64) float64 { return l(v) + right(v) }
		case '-':
			p.i++
			right, err := p.mulDiv()
			if err != nil {
				return nil, err
			}
			l := left
			left = func(v map[string]float64) float64 { return l(v) - right(v) }
		default:
			return left, nil
		}
	}
}

func (p *parser) mulDiv() (node, error) {
	left, err := p.unaryOp()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek() {
		case '*':
			p.i++
			right, err := p.unaryOp()
			if err != nil {
				return nil, err
			}
			l := left
			left = func(v map[string]float64) float64 { return l(v) * right(v) }
		case '/':
			p.i++
			right, err := p.unaryOp()
			if err != nil {
				return nil, err
			}
			l := left
			left = func(v map[string]float64) float64 { return l(v) / right(v) }
		default:
			return left, nil
		}
	}
}

// unaryOp binds looser than ^, so -2^2 is -(2^2).
func (p *parser) unaryOp() (node, error) {
	if p.peek() == '-' {
		p.i++
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer p.leave()
		n, err := p.unaryOp()
		if err != nil {
			return nil, err
		}
		return func(v map[string]float64) float64 { return -n(v) }, nil
	}
	return p.power()
}

func (p *parser) power() (node, error) {
	base, err := p.atom()
	if err != nil {
		return nil, err
	}
	if p.peek() == '^' {
		p.i++
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer p.leave()
		exp, err := p.unaryOp() // right associative
		if err != nil {
			return nil, err
		}
		return func(v map[string]float64) float64 { return math.Pow(base(v), exp(v)) }, nil
	}
	return base, nil
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > maxDepth {
		return fmt.Errorf("expression nested too deeply")
	}
	return nil
}

func (p *parser) leave() { p.depth-- }

func (p *parser) atom() (node, error) {
	c := p.peek()
	switch {
	case c == '(':
		p.i++
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer p.leave()
		n, err := p.addSub()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, fmt.Errorf("missing ) at %d", p.i)
		}
		p.i++
		return n, nil
	case c >= '0' && c <= '9' || c == '.':
		return p.number()
	case isIdentStart(c):
		return p.ident()
	case c == 0:
		return nil, fmt.Errorf("unexpected end of expression")
	}
	return nil, fmt.Errorf("unexpected %q at %d", string(c), p.i)
}

func (p *parser) number() (node, error) {
	start := p.i
	for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9' || p.s[p.i] == '.') {
		p.i++
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		j := p.i + 1
		if j < len(p.s) && (p.s[j] == '+' || p.s[j] == '-') {
			j++
		}
		if j < len(p.s) && p.s[j] >= '0' && p.s[j] <= '9' {
			for j < len(p.s) && p.s[j] >= '0' && p.s[j] <= '9' {
				j++
			}
			p.i = j
		}
	}
	v, err := strconv.ParseFloat(p.s[start:p.i], 64)
	if err != nil {
		return nil, fmt.Errorf("bad number %q", p.s[start:p.i])
	}
	return func(map[string]float64) float64 { return v }, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || unicode.IsLetter(rune(c))
}

func (p *parser) ident() (node, error) {
	start := p.i
	for p.i < len(p.s) && (isIdentStart(p.s[p.i]) || p.s[p.i] >= '0' && p.s[p.i] <= '9') {
		p.i++
	}
	name := p.s[start:p.i]
	if p.peek() == '(' {
		f, ok := functions[name]
		if !ok {
			return nil, fmt.Errorf("unknown function %q", name)
		}
		return p.call(name, f)
	}
	if !p.allowed[name] {
		return nil, fmt.Errorf("unknown identifier %q", name)
	}
	return func(v map[string]float64) float64 { return v[name] }, nil
}

func (p *parser) call(name string, f fn) (node, error) {
	p.i++ // (
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	var args []node
	for {
		a, err := p.addSub()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		c := p.peek()
		if c == ',' {
			p.i++
			continue
		}
		if c == ')' {
			p.i++
			break
		}
		return nil, fmt.Errorf("expected , or ) in %s()", name)
	}
	if len(args) < f.min || (f.max >= 0 && len(args) > f.max) {
		return nil, fmt.Errorf("wrong argument count for %s()", name)
	}
	return func(v map[string]float64) float64 {
		vals := make([]float64, len(args))
		for i, a := range args {
			vals[i] = a(v)
		}
		return f.call(vals)
	}, nil
}

// IsFunction reports whether name is reserved for a built-in function.
func IsFunction(name string) bool {
	_, ok := functions[strings.ToLower(name)]
	return ok
}
