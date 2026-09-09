import { Route, Routes } from 'react-router-dom';
import { Dashboard } from '@/components/shell/Dashboard';

export function App() {
  return (
    <Routes>
      {/* One route owns every dashboard URL so React never replaces the
          Dashboard component (and its client-side tab state) on navigation. */}
      <Route path="*" element={<Dashboard />} />
    </Routes>
  );
}
