import { Component, type ErrorInfo, type ReactNode } from "react";
import { describeError, initialBoundary, nextBoundary, type BoundaryState } from "../lib/errorBoundary";

/** Catches render errors below it so the window never goes blank; "Reload" restarts the page. */
export class ErrorBoundary extends Component<{ children: ReactNode }, BoundaryState> {
  state = initialBoundary;

  static getDerivedStateFromError(error: unknown): BoundaryState {
    return nextBoundary(initialBoundary, { type: "error", error });
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Screen crashed:", error, info.componentStack);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="screen crash" role="alert">
        <h2>This screen stopped working</h2>
        <p>{describeError(this.state.error)}</p>
        <button type="button" className="pbtn solid" onClick={() => window.location.reload()}>
          Reload
        </button>
      </div>
    );
  }
}
