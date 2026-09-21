import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import './styles.css';
createRoot(document.getElementById('root')!).render(<StrictMode>
 {import.meta.env.VITE_TEMPORARY_DEMO === 'true' && <div className="demo-notice" role="note">Temporary hiring demo. Use sample files only. Data expires with the demo database; the app may take about a minute to wake.</div>}
 <App />
</StrictMode>);
