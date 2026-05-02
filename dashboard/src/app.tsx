import {
  MutationCache,
  QueryCache,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import { Route, Router, Switch } from "wouter-preact";
import { TopBar } from "./components/TopBar";
import { ToastStack } from "./components/ToastStack";
import { HelpOverlay } from "./components/HelpOverlay";
import { Overview } from "./pages/Overview";
import { Instances } from "./pages/Instances";
import { InstanceDetail } from "./pages/InstanceDetail";
import { Plugins } from "./pages/Plugins";
import { NotFound } from "./pages/NotFound";
import { pushToast } from "./lib/toast";

// Single QueryClient for the lifetime of the app. Defaults are tuned for
// a polling dashboard: refetch on window focus, retry once, sane stale
// time so we don't hammer the daemon. Global error handlers funnel any
// query / mutation failure into the toast stack so individual pages
// don't have to remember to render their own error blocks.
const qc = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      refetchOnWindowFocus: true,
      retry: 1,
    },
  },
  queryCache: new QueryCache({
    onError: (err, query) => {
      // Suppress noise on background polls when the daemon is briefly down.
      // First failure of a fresh query → toast. Subsequent ones in a row
      // (failureCount > 1) → silence; the user already saw it.
      if (query.state.fetchFailureCount > 1) return;
      pushToast("error", `${query.queryKey.join("/")}: ${err.message}`);
    },
  }),
  mutationCache: new MutationCache({
    onError: (err) => {
      pushToast("error", err.message);
    },
  }),
});

export function App() {
  return (
    <QueryClientProvider client={qc}>
      {/* The dashboard is mounted at /ui by the daemon; we tell wouter so
          all <Link href="/instances"> resolve to /ui/instances correctly. */}
      <Router base="/ui">
        <div class="min-h-screen flex flex-col">
          <TopBar />
          <main class="flex-1 max-w-7xl w-full mx-auto px-4 py-6">
            <Switch>
              <Route path="/" component={Overview} />
              <Route path="/instances" component={Instances} />
              <Route path="/instances/:name" component={InstanceDetail} />
              <Route path="/plugins" component={Plugins} />
              <Route component={NotFound} />
            </Switch>
          </main>
          <footer class="border-t border-border text-xs text-text-muted py-2 px-4 text-center">
            <span class="font-mono">dockersnap</span> · press{" "}
            <kbd class="font-mono bg-code-bg border border-border rounded-sm px-1">
              ?
            </kbd>{" "}
            for shortcuts
          </footer>
          <ToastStack />
          <HelpOverlay />
        </div>
      </Router>
    </QueryClientProvider>
  );
}
