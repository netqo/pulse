import { Link, Route, Routes } from 'react-router-dom';

import { Playground } from './playground/Playground';

/**
 * App is the top-level shell: a header bar above the routed content. The
 * Playground serves both the home route and a shared query at /q/:id.
 */
export function App() {
  return (
    <div className="app">
      <header className="app-header">
        <Link className="brand" to="/">
          <span className="logo" aria-hidden="true" />
          <h1>Pulse</h1>
        </Link>
        <span className="subtitle">SQL Playground</span>
      </header>
      <main>
        <Routes>
          <Route path="/" element={<Playground />} />
          <Route path="/q/:id" element={<Playground />} />
          <Route path="*" element={<Playground />} />
        </Routes>
      </main>
    </div>
  );
}
