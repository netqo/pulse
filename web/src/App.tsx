import { Playground } from './playground/Playground';

/** App is the top-level shell: a header bar above the Playground workspace. */
export function App() {
  return (
    <div className="app">
      <header className="app-header">
        <div className="brand">
          <span className="logo" aria-hidden="true" />
          <h1>Pulse</h1>
        </div>
        <span className="subtitle">SQL Playground</span>
      </header>
      <main>
        <Playground />
      </main>
    </div>
  );
}
