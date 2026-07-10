import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import './monaco';
import './index.css';
import { App } from './App';
import { ErrorBoundary } from './ErrorBoundary';

const container = document.getElementById('root');
if (!container) {
  throw new Error('root element not found');
}

createRoot(container).render(
  <StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </StrictMode>,
);
