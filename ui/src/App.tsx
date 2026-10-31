import { Navigate, RouterProvider, useSearchParams } from "react-router-dom";
import { AdminShell } from "@/shell/AdminShell";
import { SignedIn } from "@/shell/SignedIn";
import { ToastProvider } from "@/shell/Toasts";
import { LiveProvider } from "@/live/LiveProvider";
import { TasksPage } from "@/domains/tasks/TasksPage";
import { PlanPage } from "@/domains/tasks/PlanPage";
import { IntegrationsPage } from "@/domains/connections/IntegrationsPage";
import { TaskPage } from "@/domains/tasks/TaskPage";
import { AgentsPage } from "@/domains/agents/AgentsPage";
import { AgentPage } from "@/domains/agents/AgentPage";
import { AgentEditPage } from "@/domains/agents/AgentEditPage";
import { PolicyPage } from "@/domains/policy/PolicyPage";
import { SettingsPage } from "@/domains/settings/SettingsPage";
import { WorkersPage } from "@/domains/workers/WorkersPage";
import { NotFoundPage } from "@/shell/NotFoundPage";
import { ErrorPage } from "@/shell/ErrorPage";
import { WorkspacePage } from "@/domains/tasks/workspace/WorkspacePage";
import { EXTENSIONS } from "@/extensions";
import { createRouter, Frame } from "@/lib/host";

// Every page renders inside the one shell. The Go server answers any path
// that isn't the API or a file with index.html, so these addresses can be
// bookmarked and reloaded. An extension's routes are there whether or not it
// shows, so a bookmark to one still opens.
const router = createRouter([
  // A task's Workspace is its own page, in its own tab.
  { path: "/tasks/:taskId/workspace", element: <WorkspacePage />, errorElement: <ErrorPage /> },
  {
    element: <AdminShell />,
    children: [
      {
        errorElement: <ErrorPage />,
        children: [
          { path: "/", element: <Navigate to="/tasks" replace /> },
          { path: "/tasks", element: <TasksPage /> },
          { path: "/tasks/new", element: <NewTaskLink /> },
          { path: "/plan", element: <PlanPage /> },
          { path: "/tasks/:taskId", element: <TaskPage /> },
          { path: "/agents", element: <AgentsPage /> },
          { path: "/agents/new", element: <AgentEditPage /> },
          { path: "/agents/:name", element: <AgentPage /> },
          { path: "/agents/:name/edit", element: <AgentEditPage /> },
          { path: "/workers", element: <WorkersPage /> },
          { path: "/integrations", element: <IntegrationsPage /> },
          { path: "/policy", element: <PolicyPage /> },
          { path: "/settings", element: <SettingsPage /> },
          ...EXTENSIONS.flatMap((extension) => extension.routes ?? []),
          { path: "*", element: <NotFoundPage /> },
        ],
      },
    ],
  },
]);

// NewTaskLink keeps old links to the New task page working: it is a modal
// over the Tasks page now (lib/new-task).
function NewTaskLink() {
  const [params] = useSearchParams();
  const epic = params.get("epic");
  const kind = epic ? "story" : params.get("kind") === "epic" ? "epic" : "task";
  return <Navigate to={`/tasks?new=${kind}${epic ? `&newIn=${encodeURIComponent(epic)}` : ""}`} replace />;
}

export function App() {
  return (
    <SignedIn>
      <LiveProvider>
        <ToastProvider>
          <Frame>
            <RouterProvider router={router} />
          </Frame>
        </ToastProvider>
      </LiveProvider>
    </SignedIn>
  );
}
