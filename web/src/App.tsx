import { Suspense } from 'react';
import { Navigate, Route, Routes, useLocation } from 'react-router-dom';

import RequireAuth from './auth/RequireAuth';
import { LibrariesProvider } from './api/libraries';
import AppShell from './components/app-shell/AppShell';
import { ToastProvider } from './components/ui/Toast';
import { lazyPage } from './lib/lazyPage';
import { UploadProvider } from './components/upload/UploadProvider';
import FavoritesPage from './pages/FavoritesPage';
import FilesPage from './pages/FilesPage';
import ForbiddenPage from './pages/ForbiddenPage';
import HomePage from './pages/HomePage';
import LandingPage from './pages/LandingPage';
import LoginPage from './pages/LoginPage';
import SearchPage from './pages/SearchPage';
import SetupPage from './pages/SetupPage';
import TrashPage from './pages/TrashPage';

/* Pages most visits never touch load on demand, keeping the first load small. */
const BackupsPage = lazyPage(() => import('./pages/BackupsPage'));
const DuplicatesPage = lazyPage(() => import('./pages/DuplicatesPage'));
const LibrariesPage = lazyPage(() => import('./pages/LibrariesPage'));
const PermissionsPage = lazyPage(() => import('./pages/PermissionsPage'));
const SharingPage = lazyPage(() => import('./pages/SharingPage'));
const TagsPage = lazyPage(() => import('./pages/TagsPage'));
const SettingsPage = lazyPage(() => import('./pages/SettingsPage'));
const PublicSharePage = lazyPage(() => import('./pages/PublicSharePage'));

/** `/browse?q=…` became `/search?q=…`. */
function LegacyBrowseRedirect() {
  const { search } = useLocation();
  return <Navigate to={`/search${search}`} replace />;
}

/** Shown for the instant a lazily loaded page is fetched. */
function RouteFallback() {
  return (
    <p className="visually-hidden" role="status">
      Loading…
    </p>
  );
}

export default function App() {
  return (
    <LibrariesProvider>
      <ToastProvider>
        <UploadProvider>
          <Suspense fallback={<RouteFallback />}>
            <Routes>
              {/* Public routes */}
              <Route path="/landing" element={<LandingPage />} />
              <Route path="/s/:token" element={<PublicSharePage />} />
              <Route path="/setup" element={<SetupPage />} />
              <Route path="/signup" element={<SetupPage />} />
              <Route path="/login" element={<LoginPage />} />

              <Route element={<RequireAuth />}>
                <Route path="/403" element={<ForbiddenPage />} />
                <Route element={<AppShell />}>
                  <Route path="/" element={<HomePage />} />

                  <Route path="/files" element={<FilesPage />} />
                  <Route path="/search" element={<SearchPage />} />
                  <Route path="/browse" element={<LegacyBrowseRedirect />} />

                  <Route path="/tags" element={<TagsPage />} />
                  <Route path="/favorites" element={<FavoritesPage />} />
                  <Route path="/trash" element={<TrashPage />} />
                  <Route path="/shared" element={<SharingPage />} />
                  <Route path="/duplicates" element={<DuplicatesPage />} />

                  <Route path="/libraries" element={<LibrariesPage />} />
                  <Route path="/permissions" element={<PermissionsPage />} />
                  <Route path="/backups" element={<BackupsPage />} />
                  <Route path="/settings" element={<SettingsPage />} />
                </Route>
              </Route>

              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </Suspense>
        </UploadProvider>
      </ToastProvider>
    </LibrariesProvider>
  );
}
