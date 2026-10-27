import { type ChildProcess, execFileSync, execSync, spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { waitForHealth } from "./backend";
import { killProcessGroup } from "./process-group";
import { makeGitEnv } from "../helpers/git-helper";
import { dwell } from "../helpers/causal-waits";
import { ApiClient } from "../helpers/api-client";
import { waitForAgentMessage, waitForSessionDone } from "../helpers/session";

const BACKEND_DIR = path.resolve(__dirname, "../../../../apps/backend");
const WEB_DIR = path.resolve(__dirname, "../..");
// The remote instance runs the same prebuilt binary the default backend
// fixture spawns (KANDEV_E2E_BIN selects a custom one for both).
const KANDEV_BIN = process.env.KANDEV_E2E_BIN || path.join(BACKEND_DIR, "bin", "kandev");
const WEB_DIST_DIR = path.join(WEB_DIR, "dist");

const HEALTH_TIMEOUT_MS = 30_000;

/**
 * The version the remote copy reports. It must parse at or above
 * `instances.MinRemoteVersion` ("0.98.0" in apps/backend/internal/instances/health.go),
 * because the local backend's health probe marks a lower core triple
 * unsupported. The build's own `git describe` (or CI's "0.0.0-e2e.<sha>")
 * is below the floor mid-development, so the fixture stamps the copy.
 */
const STAMPED_VERSION_TRIPLE = "0.98.0";

export type RemoteBackendContext = {
  port: number;
  baseUrl: string;
  tmpDir: string;
  pid: () => number | undefined;
  /** Kill the remote process group; persisted state and tmpDir survive. */
  kill: () => Promise<void>;
  /** Kill (if still running) and remove the disposable tmpDir. */
  dispose: () => Promise<void>;
};

export type RemoteBackendSeed = {
  client: ApiClient;
  workspaceId: string;
  workflowId: string;
  startStepId: string;
  repositoryId: string;
  agentProfileId: string;
  task: { id: string; session_id: string };
};

export type BootRemoteBackendOptions = {
  /** Base URL of the already-running local backend; its reported build version is stamped onto the remote copy. */
  localBaseUrl: string;
  /** The local fixture's port-offset/slot inputs, derived via remoteBackendPortInputs. */
  portOffset: number;
  parallelSlot: number;
};

/**
 * Derive the port-offset/slot pair the default backend fixture used, from the
 * port it published on `backend.port`. `helpers/backend-ports` computes
 * `18080 + offset + slot * 30` with offset in [0, 29] and slot in [0, 4], so
 * the decomposition below is exact and no fixture export is needed.
 */
export function remoteBackendPortInputs(backendPort: number): {
  portOffset: number;
  parallelSlot: number;
} {
  const relative = backendPort - 18080;
  if (!Number.isInteger(relative) || relative < 0 || relative > 149) {
    throw new Error(`remote backend fixture: unexpected local backend port ${backendPort}`);
  }
  return { portOffset: relative % 30, parallelSlot: Math.floor(relative / 30) };
}

/**
 * The remote instance's port band. Every region is disjoint from the local
 * fixture's for ANY (offset, slot) pair, including across concurrent runs:
 * the local backend uses 18080-18229 (HTTP), 19080-19229 (standalone
 * agentctl), and 30001-62000 (agentctl instance blocks); the remote uses
 * 21000-21149, 22000-22149, and 24001-29730 respectively.
 */
function remoteBackendPorts(offset: number, slot: number) {
  const backendPort = 21000 + offset + slot * 30;
  const standaloneAgentctlPort = backendPort + 1000;
  const agentctlPortBase = 24001 + offset * 190 + slot * 30;
  return {
    backendPort,
    standaloneAgentctlPort,
    agentctlPortBase,
    agentctlPortMax: agentctlPortBase + 99,
  };
}

/** Read the version the local backend reports; the remote copy shares its binary. */
async function readLocalBackendVersion(localBaseUrl: string): Promise<string> {
  const res = await fetch(`${localBaseUrl}/api/v1/system/info`);
  if (!res.ok) {
    throw new Error(`remote backend fixture: local system info returned ${res.status}`);
  }
  const info = (await res.json()) as { version?: string };
  const version = info.version?.trim();
  if (!version) {
    throw new Error("remote backend fixture: local system info reported no version");
  }
  return version;
}

/**
 * Copy the backend binary and rewrite its embedded version string to a
 * same-length value whose core triple meets the remote version floor.
 *
 * `make` stamps `main.Version` through ldflags, and the CI e2e shards run the
 * prebuilt binary with no Go toolchain, so the fixture cannot rebuild with
 * `VERSION=`. The string is instead patched in a byte-identical-length copy
 * (Go string headers keep their length; the health parser cuts at the first
 * "-", so the dash padding is inert). Modifying a Mach-O invalidates its
 * ad-hoc code signature, so darwin copies are re-signed ad hoc.
 */
function stampVersionedBinary(
  sourcePath: string,
  targetPath: string,
  reportedVersion: string,
): void {
  fs.copyFileSync(sourcePath, targetPath);
  const binary = fs.readFileSync(targetPath);
  const needle = Buffer.from(reportedVersion, "latin1");
  let stampedHead = `v${STAMPED_VERSION_TRIPLE}-e2e`;
  if (needle.length < stampedHead.length) stampedHead = `v${STAMPED_VERSION_TRIPLE}`;
  if (needle.length < stampedHead.length) {
    throw new Error(
      `remote backend fixture: build version "${reportedVersion}" is too short to stamp ` +
        `(needs at least ${stampedHead.length} bytes for "${stampedHead}")`,
    );
  }
  const replacement = Buffer.alloc(needle.length, 0x2d);
  replacement.write(stampedHead, 0, "latin1");

  let index = binary.indexOf(needle);
  let patched = 0;
  while (index !== -1) {
    replacement.copy(binary, index);
    patched += 1;
    index = binary.indexOf(needle, index + needle.length);
  }
  if (patched === 0) {
    throw new Error(
      `remote backend fixture: build version "${reportedVersion}" not found in ${sourcePath}`,
    );
  }
  fs.writeFileSync(targetPath, binary);
  if (process.platform === "darwin") {
    execFileSync("codesign", ["--force", "--sign", "-", targetPath], { stdio: "pipe" });
  }
}

/**
 * Boot the second backend instance for multi-instance E2E flows: an isolated
 * HOME/database/tmpDir, its own port band, the e2e mock profile, and a
 * version-stamped copy of the prebuilt binary so the local instance's health
 * probe reports the remote as supported. The remote never enables the
 * multi-instance feature itself; it is the unmodified remote side of the
 * federation (occurrences REST + notification frames).
 */
export async function bootRemoteBackend(
  options: BootRemoteBackendOptions,
): Promise<RemoteBackendContext> {
  const { localBaseUrl, portOffset, parallelSlot } = options;
  const ports = remoteBackendPorts(portOffset, parallelSlot);
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), `kandev-e2e-remote-${parallelSlot}-`));
  const homeDir = path.join(tmpDir, ".kandev");
  const systemTemporaryRoot = path.join(tmpDir, "system-temporary");
  const dbPath = path.join(tmpDir, "kandev.db");
  const worktreeBase = path.join(tmpDir, "worktrees");
  const repoCloneBase = path.join(tmpDir, "managed-repos");
  const processLogPath = path.join(tmpDir, "backend-process.log");
  for (const dir of [homeDir, systemTemporaryRoot, worktreeBase, repoCloneBase]) {
    fs.mkdirSync(dir, { recursive: true });
  }
  fs.writeFileSync(
    path.join(tmpDir, ".gitconfig"),
    "[user]\n  name = E2E Remote\n  email = e2e-remote@test.local\n[commit]\n  gpgsign = false\n[tag]\n  gpgsign = false\n",
  );

  // Deliberately NOT a spread of process.env: this suite often runs inside a
  // kandev task whose shell exports KANDEV_HOME_DIR, service markers and
  // agent ports, none of which may reach the isolated remote instance.
  // TMPDIR is the one passthrough that matters: without it Go's os.TempDir()
  // falls back to /tmp, which is a symlink on macOS and the temp-artifacts
  // safety anchor rejects it, so the host-utility manager never stages the
  // mock-agent binary and every agent launch fails.
  const originalPath = process.env.PATH ?? "";
  const backendEnv = {
    PATH: [path.join(BACKEND_DIR, "bin"), originalPath].filter(Boolean).join(path.delimiter),
    ...(process.env.TMPDIR ? { TMPDIR: process.env.TMPDIR } : {}),
    HOME: tmpDir,
    KANDEV_HOME_DIR: homeDir,
    KANDEV_SERVER_PORT: String(ports.backendPort),
    KANDEV_WEB_DIST_DIR: WEB_DIST_DIR,
    KANDEV_DATABASE_PATH: dbPath,
    KANDEV_E2E_SYSTEM_TEMP_ROOT: systemTemporaryRoot,
    KANDEV_E2E_MOCK: "true",
    // The SPA hydrates its feature flags from the ACTIVE instance's
    // app-state, and the federation UI (instance picker, notifications
    // module) gates on them. A remote with the flag off therefore hides the
    // picker while being viewed, stranding the panel on the remote with no
    // switch-back affordance. A same-release federated remote runs the flag
    // on, so the fixture does too; B's own registry stays empty, so its
    // proxy/relay managers are inert.
    KANDEV_FEATURES_MULTI_INSTANCE: "true",
    KANDEV_DOCKER_ENABLED: "false",
    KANDEV_WORKTREE_ENABLED: "true",
    KANDEV_WORKTREE_BASEPATH: worktreeBase,
    KANDEV_REPOCLONE_BASEPATH: repoCloneBase,
    KANDEV_LOG_LEVEL: process.env.KANDEV_LOG_LEVEL ?? "info",
    KANDEV_GITLAB_REMOTE_URL: `http://localhost:${ports.backendPort}/platform/kandev.git`,
    AGENTCTL_PORT: String(ports.standaloneAgentctlPort),
    AGENTCTL_INSTANCE_PORT_BASE: String(ports.agentctlPortBase),
    AGENTCTL_INSTANCE_PORT_MAX: String(ports.agentctlPortMax),
    GIT_AUTHOR_NAME: "E2E Remote",
    GIT_AUTHOR_EMAIL: "e2e-remote@test.local",
    GIT_COMMITTER_NAME: "E2E Remote",
    GIT_COMMITTER_EMAIL: "e2e-remote@test.local",
  } as Record<string, string>;

  const debug = !!process.env.E2E_DEBUG;
  const baseUrl = `http://localhost:${ports.backendPort}`;
  let proc: ChildProcess | undefined;
  let disposed = false;

  try {
    const version = await readLocalBackendVersion(localBaseUrl);
    const binaryPath = path.join(tmpDir, "kandev-remote");
    stampVersionedBinary(KANDEV_BIN, binaryPath, version);
    // The agent registry resolves the mock-agent command to the directory of
    // the RUNNING binary (configureMockAgent uses filepath.Dir(os.Executable())),
    // not PATH, so the remote copy needs its own mock-agent beside it.
    fs.copyFileSync(path.join(BACKEND_DIR, "bin", "mock-agent"), path.join(tmpDir, "mock-agent"));

    proc = spawn(binaryPath, ["__backend"], {
      env: backendEnv,
      stdio: ["ignore", "pipe", "pipe"],
      detached: true,
    });
    const logFile = fs.createWriteStream(processLogPath, { flags: "a" });
    const closeLogFile = () => {
      if (!logFile.writableEnded) logFile.end();
    };
    proc.once("close", closeLogFile);
    proc.once("error", closeLogFile);
    proc.stderr?.on("data", (chunk: Buffer) => {
      logFile.write(chunk);
      if (debug) process.stderr.write(`[remote-backend:${ports.backendPort}] ${chunk.toString()}`);
    });
    proc.stdout?.on("data", (chunk: Buffer) => {
      logFile.write(chunk);
      if (debug)
        process.stderr.write(`[remote-backend-log:${ports.backendPort}] ${chunk.toString()}`);
    });

    await waitForHealth(`${baseUrl}/ready`, HEALTH_TIMEOUT_MS, proc);
  } catch (error) {
    if (proc && proc.exitCode === null && proc.signalCode === null) {
      await killProcessGroup(proc).catch(() => undefined);
    }
    removeRemoteTempRoot(tmpDir);
    throw error;
  }

  const runningProcess = proc;
  const kill = async () => {
    if (!runningProcess) return;
    if (runningProcess.exitCode === null && runningProcess.signalCode === null) {
      await killProcessGroup(runningProcess);
    }
  };
  const dispose = async () => {
    if (disposed) return;
    disposed = true;
    await kill().catch(() => undefined);
    removeRemoteTempRoot(tmpDir);
  };

  return {
    port: ports.backendPort,
    baseUrl,
    tmpDir,
    pid: () => runningProcess?.pid,
    kill,
    dispose,
  };
}

function removeRemoteTempRoot(tmpDir: string): void {
  fs.rmSync(tmpDir, { recursive: true, force: true, maxRetries: 3, retryDelay: 100 });
}

/**
 * Seed the remote instance with the minimal graph the multi-instance flows
 * need: a workspace + workflow a board can boot to, a local git repository
 * the mock agent's session can use, and one agent-backed task whose unique
 * title distinguishes the remote board from the local one. The task's first
 * mock turn is driven to completion so later flows start from an idle
 * session and can trigger further turns on demand.
 */
export async function seedRemoteBackendTask(
  remote: RemoteBackendContext,
  title: string,
): Promise<RemoteBackendSeed> {
  const client = new ApiClient(remote.baseUrl);
  const workspace = await client.createWorkspace("Remote Instance Workspace");
  const workflow = await client.createWorkflow(workspace.id, "Remote Workflow", "simple");
  const { steps } = await client.listWorkflowSteps(workflow.id);
  const sorted = steps.sort((a, b) => a.position - b.position);
  const startStep = sorted.find((step) => step.is_start_step) ?? sorted[0];

  const repoDir = path.join(remote.tmpDir, "repos", "remote-repo");
  fs.mkdirSync(repoDir, { recursive: true });
  const gitEnv = makeGitEnv(remote.tmpDir);
  execSync("git init -b main", { cwd: repoDir, env: gitEnv });
  fs.writeFileSync(path.join(repoDir, "remote-seed.txt"), "remote seed\n");
  execSync("git add remote-seed.txt", { cwd: repoDir, env: gitEnv });
  execSync('git commit -m "init"', { cwd: repoDir, env: gitEnv });
  const repository = await client.createRepository(workspace.id, repoDir);

  // Agent registry seeding is warn-only-failure at boot, so poll for the
  // mock agent profile exactly like the local seedData fixture does.
  let agentProfileId: string | undefined;
  const agentsDeadline = Date.now() + 30_000;
  while (Date.now() < agentsDeadline) {
    const { agents } = await client.listAgents();
    agentProfileId = agents
      .filter((agent) => agent.id !== "dynamic")
      .flatMap((agent) => agent.profiles ?? [])[0]?.id;
    if (agentProfileId) break;
    await dwell(
      250,
      "poll-interval",
      "sampling interval for the remote instance's seeded-agent-profile poll; seeding is asynchronous backend work read back over HTTP, with no page in this fixture",
    );
  }
  if (!agentProfileId) {
    throw new Error("remote backend fixture: no agent profile available on the remote instance");
  }

  // The remote's notification delivery (and therefore the relay's live
  // event stream) only fires for events a provider is subscribed to. The
  // lazily created default providers cover clarification/inbox/update but
  // not session.turn_finished, so opt the remote's user into turn-finished
  // notifications the way a real federated remote would.
  const providerResponse = await client.rawRequest("POST", "/api/v1/notification-providers", {
    name: "E2E Remote Turn Notifications",
    type: "local",
    config: {},
    enabled: true,
    events: ["session.turn_finished"],
  });
  if (providerResponse.status !== 200) {
    throw new Error(`remote backend fixture: provider create returned ${providerResponse.status}`);
  }

  // Point the remote default user's board at the seeded graph, mirroring the
  // settings the local testPage fixture resets before every test.
  await client.saveUserSettings({
    workspace_id: workspace.id,
    workflow_filter_id: workflow.id,
    startup_page: "task_overview",
    kanban_view_mode: "",
    tasks_list_sort: "updated_desc",
    tasks_list_group: "state",
    message_time_display: "relative",
  });

  const task = await client.createTaskWithAgent(workspace.id, title, agentProfileId, {
    description: "/e2e:simple-message",
    workflow_id: workflow.id,
    workflow_step_id: startStep.id,
    repository_ids: [repository.id],
  });
  if (!task.session_id) {
    throw new Error("remote backend fixture: createTaskWithAgent did not return a session_id");
  }
  await waitForAgentMessage(client, task.session_id, "simple mock response", 30_000);
  await waitForSessionDone(
    client,
    task.id,
    task.session_id,
    "Waiting for the remote fixture turn to finish",
    30_000,
  );

  return {
    client,
    workspaceId: workspace.id,
    workflowId: workflow.id,
    startStepId: startStep.id,
    repositoryId: repository.id,
    agentProfileId,
    task: { id: task.id, session_id: task.session_id },
  };
}
