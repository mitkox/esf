import React from "react";

export class ErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error) {
    return { error };
  }

  componentDidCatch(error, info) {
    // Keep the diagnostic in the browser console; the UI stays usable.
    console.error("Console page failed to render", error, info);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="mx-auto w-full max-w-[1600px] px-4 py-10 sm:px-6 lg:px-8">
          <div className="mx-auto max-w-xl rounded-panel border border-danger/30 bg-danger/5 p-6">
            <h2 className="text-base font-semibold text-danger">This screen could not be rendered</h2>
            <p className="mt-2 text-sm text-muted-foreground">
              The console caught an unexpected rendering error. The rest of the application is still available.
            </p>
            <pre className="mt-4 max-h-40 overflow-auto rounded-lg bg-background/70 p-3 text-xs text-muted-foreground scroll-thin">
              {String(this.state.error?.message || this.state.error)}
            </pre>
            <button
              type="button"
              className="mt-4 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm font-medium hover:bg-muted"
              onClick={() => this.setState({ error: null })}
            >
              Try again
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
