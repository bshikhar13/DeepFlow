import { Component, type ReactNode } from "react";

interface State { failed: boolean }

// CardBoundary keeps one card's crash inside that card, so the rest of the
// workspace keeps working.
export class CardBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { failed: false };

  static getDerivedStateFromError(): State {
    return { failed: true };
  }

  componentDidCatch(error: unknown) {
    console.error("A card crashed:", error);
  }

  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <div className="card-crash" role="alert">
        <p>This card hit a problem.</p>
        <button onClick={() => this.setState({ failed: false })}>Try again</button>
      </div>
    );
  }
}
