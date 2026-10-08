/**
 * The providers every route in the app sits inside.
 *
 * Kept in its own file so a component test that needs its own `<Routes>` — the
 * app shell, which renders an `Outlet` — can get the same context
 * `renderPage` provides instead of hand-assembling the providers and drifting.
 */

import type { ReactNode } from 'react';

import { LibrariesProvider } from '../api/libraries';
import AuthProvider from '../auth/AuthProvider';
import { ToastProvider } from '../components/ui/Toast';
import { UploadProvider } from '../components/upload/UploadProvider';

export default function Providers({ children }: { children: ReactNode }) {
  return (
    <AuthProvider>
      <LibrariesProvider>
        <ToastProvider>
          <UploadProvider>{children}</UploadProvider>
        </ToastProvider>
      </LibrariesProvider>
    </AuthProvider>
  );
}
