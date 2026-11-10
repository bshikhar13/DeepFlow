const COLOURS = ["#ff6b9d", "#ffb020", "#34d399", "#38bdf8", "#a78bfa"];

// Confetti is a small burst of CSS-animated pieces. Reduced-motion hides it.
export function Confetti({ pieces = 16 }: { pieces?: number }) {
  return (
    <div className="confetti" aria-hidden="true">
      {Array.from({ length: pieces }, (_, i) => (
        <span key={i} style={{ left: `${(i * 97) % 100}%`, background: COLOURS[i % COLOURS.length], animationDelay: `${(i % 5) * 40}ms` }} />
      ))}
    </div>
  );
}
