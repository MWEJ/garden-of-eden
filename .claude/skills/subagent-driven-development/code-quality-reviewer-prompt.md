# Code Quality Reviewer Prompt Template

Use this template when dispatching a code quality reviewer subagent.

**Purpose:** Verify implementation is well-built (clean, tested, maintainable)

**Only dispatch after spec compliance review passes.**

```
Task tool (general-purpose):
  Use template at requesting-code-review/code-reviewer.md

  DESCRIPTION: [task summary, from implementer's report]
  PLAN_OR_REQUIREMENTS: Task N from [plan-file]
  CHANGED_FILES: [file list from implementer's report]
```

**Tasks are not committed individually** — the plan gets a single commit at the end. So instead of a SHA range, tell the reviewer to inspect the task's files directly:

```bash
git diff HEAD -- [changed files]   # modifications to tracked files
```

plus read any new (untracked) files in full. For the final whole-plan review, dispatched after the single plan commit, use the template's normal `BASE_SHA`/`HEAD_SHA` range (base = commit before the plan commit).

**In addition to standard code quality concerns, the reviewer should check:**
- Does each file have one clear responsibility with a well-defined interface?
- Are units decomposed so they can be understood and tested independently?
- Is the implementation following the file structure from the plan?
- Did this implementation create new files that are already large, or significantly grow existing files? (Don't flag pre-existing file sizes — focus on what this change contributed.)

**Code reviewer returns:** Strengths, Issues (Critical/Important/Minor), Assessment
