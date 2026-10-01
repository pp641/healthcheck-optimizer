"use client";

export default function AppError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <div className="card empty stack" style={{ alignItems: "center" }}>
      <h2>Couldn&apos;t load this page</h2>
      <p className="subtle">{error.message || "The Tidyfleet server did not respond."}</p>
      <button className="btn" onClick={reset}>
        Try again
      </button>
    </div>
  );
}
