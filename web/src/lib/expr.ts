// Browser evaluator for the safe expression language. It mirrors
// internal/expr/expr.go and shares its conformance cases. Expressions are
// parsed by hand and never reach eval or the Function constructor.

const MAX_LEN = 200;
const MAX_DEPTH = 32;

type Vars = Record<string, number>;
type Node = (v: Vars) => number;

const FUNCTIONS = new Map<string, { min: number; max: number; call: (a: number[]) => number }>([
  ["min", { min: 1, max: Infinity, call: (a) => Math.min(...a) }],
  ["max", { min: 1, max: Infinity, call: (a) => Math.max(...a) }],
  ["abs", { min: 1, max: 1, call: (a) => Math.abs(a[0]) }],
  ["sqrt", { min: 1, max: 1, call: (a) => Math.sqrt(a[0]) }],
  ["log", { min: 1, max: 1, call: (a) => Math.log(a[0]) }],
  ["log2", { min: 1, max: 1, call: (a) => Math.log2(a[0]) }],
  ["log10", { min: 1, max: 1, call: (a) => Math.log10(a[0]) }],
  ["exp", { min: 1, max: 1, call: (a) => Math.exp(a[0]) }],
  ["floor", { min: 1, max: 1, call: (a) => Math.floor(a[0]) }],
  ["ceil", { min: 1, max: 1, call: (a) => Math.ceil(a[0]) }],
]);

class Parser {
  private i = 0;
  private depth = 0;
  constructor(private s: string, private allowed: Set<string>) {}

  parse(): Node {
    const n = this.addSub();
    if (this.peek() !== "") throw new Error(`unexpected "${this.s.slice(this.i)}"`);
    return n;
  }

  private peek(): string {
    while (this.s[this.i] === " " || this.s[this.i] === "\t") this.i++;
    return this.s[this.i] ?? "";
  }

  private enter() {
    if (++this.depth > MAX_DEPTH) throw new Error("expression nested too deeply");
  }

  private addSub(): Node {
    let left = this.mulDiv();
    for (;;) {
      const c = this.peek();
      if (c !== "+" && c !== "-") return left;
      this.i++;
      const l = left;
      const r = this.mulDiv();
      left = c === "+" ? (v) => l(v) + r(v) : (v) => l(v) - r(v);
    }
  }

  private mulDiv(): Node {
    let left = this.unary();
    for (;;) {
      const c = this.peek();
      if (c !== "*" && c !== "/") return left;
      this.i++;
      const l = left;
      const r = this.unary();
      left = c === "*" ? (v) => l(v) * r(v) : (v) => l(v) / r(v);
    }
  }

  // Binds looser than ^, so -2^2 is -(2^2).
  private unary(): Node {
    if (this.peek() === "-") {
      this.i++;
      this.enter();
      const n = this.unary();
      this.depth--;
      return (v) => -n(v);
    }
    return this.power();
  }

  private power(): Node {
    const base = this.atom();
    if (this.peek() === "^") {
      this.i++;
      this.enter();
      const exp = this.unary(); // right associative
      this.depth--;
      return (v) => Math.pow(base(v), exp(v));
    }
    return base;
  }

  private atom(): Node {
    const c = this.peek();
    if (c === "(") {
      this.i++;
      this.enter();
      const n = this.addSub();
      this.depth--;
      if (this.peek() !== ")") throw new Error("missing )");
      this.i++;
      return n;
    }
    if (/[0-9.]/.test(c)) return this.number();
    if (/[A-Za-z_]/.test(c)) return this.ident();
    throw new Error(c === "" ? "unexpected end of expression" : `unexpected "${c}"`);
  }

  private number(): Node {
    const m = /^[0-9.]+(?:[eE][+-]?[0-9]+)?/.exec(this.s.slice(this.i));
    const text = m ? m[0] : "";
    this.i += text.length;
    if (!/^(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$/.test(text)) throw new Error(`bad number "${text}"`);
    const value = Number(text);
    return () => value;
  }

  private ident(): Node {
    const m = /^[A-Za-z_][A-Za-z0-9_]*/.exec(this.s.slice(this.i));
    const name = m ? m[0] : "";
    this.i += name.length;
    if (this.peek() === "(") {
      const f = FUNCTIONS.get(name);
      if (!f) throw new Error(`unknown function "${name}"`);
      return this.call(name, f);
    }
    if (!this.allowed.has(name)) throw new Error(`unknown identifier "${name}"`);
    return (v) => v[name];
  }

  private call(name: string, f: { min: number; max: number; call: (a: number[]) => number }): Node {
    this.i++; // (
    this.enter();
    const args: Node[] = [];
    for (;;) {
      args.push(this.addSub());
      const c = this.peek();
      if (c === ",") { this.i++; continue; }
      if (c === ")") { this.i++; break; }
      throw new Error(`expected , or ) in ${name}()`);
    }
    this.depth--;
    if (args.length < f.min || args.length > f.max) throw new Error(`wrong argument count for ${name}()`);
    return (v) => f.call(args.map((a) => a(v)));
  }
}

export function parseExpr(src: string, allowed: string[]): Node {
  if (src.length > MAX_LEN) throw new Error(`expression longer than ${MAX_LEN} characters`);
  return new Parser(src, new Set(allowed)).parse();
}

export function evalExpr(src: string, vars: Vars): number {
  return parseExpr(src, Object.keys(vars))(vars);
}
