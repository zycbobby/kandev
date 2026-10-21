import { execFileSync } from "node:child_process";
import { dwell } from "../../helpers/causal-waits";
import type { BackendContext } from "../../fixtures/backend";
import { resetSeedRepositoryCheckout, type SeedData } from "../../fixtures/test-base";
import type { ApiClient } from "../../helpers/api-client";
import { GitHelper, makeGitEnv } from "../../helpers/git-helper";

/**
 * Worktree creation-base comparison fixtures.
 *
 * The scenario reproduces the "pushed 39" shape: the seed repository's local
 * main is advanced by three commits that are never pushed, so origin/main
 * stays behind. A fresh worktree task is cut from the advanced local main and
 * receives one commit of its own, which is the only work the Changes panel
 * should attribute to it once the creation base is in effect.
 */

export const creationBaseMotherCommit = "Advance local main ahead of origin";
export const creationBaseOwnCommit = "Add worktree own work";

export type WorktreeCreationBaseScenario = {
  task: { id: string };
  sessionId: string;
  worktreePath: string;
  worktreeID: string;
};

/** Return the worker fixture repository to its seeded state. */
export function restoreWorktreeCreationBaseRepository(
  seedData: SeedData,
  backend: BackendContext,
): void {
  resetSeedRepositoryCheckout(seedData, backend.tmpDir);
  const gitEnv = makeGitEnv(backend.tmpDir);
  const git = new GitHelper(seedData.repositoryPath, gitEnv);
  for (const branch of ["feature/creation-base-legacy", "fallback-base"]) {
    try {
      execFileSync("git", ["-C", seedData.repositoryPath, "branch", "-D", branch], {
        env: gitEnv,
        stdio: "ignore",
      });
    } catch {
      // The branch may not exist when setup failed early.
    }
  }
  git.exec("git clean -fd");
}

/**
 * Advance the seed repository's local main by three unpushed commits and
 * create the local branch the manual picker override selects.
 */
function advanceSeedMainAheadOfOrigin(seedData: SeedData, backend: BackendContext): void {
  const gitEnv = makeGitEnv(backend.tmpDir);
  const git = new GitHelper(seedData.repositoryPath, gitEnv);
  for (const [name, content] of [
    ["mother-one.txt", "mother change one\n"],
    ["mother-two.txt", "mother change two\n"],
    ["mother-three.txt", "mother change three\n"],
  ]) {
    git.createFile(name, content);
  }
  git.stageAll();
  git.commit(creationBaseMotherCommit);
  // A distinct local branch the picker can select to prove the manual
  // override escapes the creation-base default.
  git.exec("git branch fallback-base origin/main");
}

async function pollWorktreePath(
  apiClient: ApiClient,
  taskId: string,
): Promise<{ path: string; id: string }> {
  const deadline = Date.now() + 90_000;
  for (;;) {
    const environment = await apiClient.getTaskEnvironment(taskId);
    const worktreePath = environment?.repos?.[0]?.worktree_path;
    const worktreeID = environment?.repos?.[0]?.worktree_id;
    if (environment?.status === "ready" && worktreePath) {
      return { path: worktreePath, id: worktreeID ?? "" };
    }
    if (Date.now() > deadline) {
      const sessions = await apiClient.listTaskSessions(taskId).catch(() => null);
      throw new Error(
        `task environment did not become ready with a worktree: status=${environment?.status} repos=${JSON.stringify(environment?.repos)} sessions=${JSON.stringify(sessions)}`,
      );
    }
    await dwell(500, "poll-interval", "task environment readiness poll");
  }
}

export async function seedWorktreeCreationBaseTask(
  apiClient: ApiClient,
  seedData: SeedData,
  backend: BackendContext,
  options: { legacyCheckoutBranch?: string } = {},
): Promise<WorktreeCreationBaseScenario> {
  restoreWorktreeCreationBaseRepository(seedData, backend);
  advanceSeedMainAheadOfOrigin(seedData, backend);
  const gitEnv = makeGitEnv(backend.tmpDir);

  if (options.legacyCheckoutBranch) {
    // An already-existing remote checkout branch takes the legacy path: the
    // worktree checks the branch out instead of cutting a new one, so no
    // creation base is recorded and the comparison stays origin-first.
    const git = new GitHelper(seedData.repositoryPath, gitEnv);
    git.exec(`git checkout -B ${options.legacyCheckoutBranch} main`);
    git.exec(`git push origin ${options.legacyCheckoutBranch}`);
    git.exec("git checkout main");
  }

  const task = await apiClient.createTask(
    seedData.workspaceId,
    "Worktree creation base comparison",
    {
      description: "/e2e:simple-message",
      agent_profile_id: seedData.agentProfileId,
      executor_profile_id: seedData.worktreeExecutorProfileId,
      workflow_id: seedData.workflowId,
      workflow_step_id: seedData.startStepId,
      repositories: [
        {
          repository_id: seedData.repositoryId,
          base_branch: "main",
          ...(options.legacyCheckoutBranch
            ? { checkout_branch: options.legacyCheckoutBranch }
            : {}),
        },
      ],
    },
  );

  const launch = await apiClient.launchSession({
    task_id: task.id,
    agent_profile_id: seedData.agentProfileId,
    executor_profile_id: seedData.worktreeExecutorProfileId,
    workflow_step_id: seedData.startStepId,
    prompt: "/e2e:simple-message",
  });
  const sessionId = launch.session_id;

  const { path: worktreePath, id: worktreeID } = await pollWorktreePath(apiClient, task.id);
  const worktreeGit = new GitHelper(worktreePath, gitEnv);
  worktreeGit.createFile("own-work.txt", "worktree own work\n");
  worktreeGit.stageAll();
  worktreeGit.commit(creationBaseOwnCommit);

  return { task, sessionId, worktreePath, worktreeID };
}
