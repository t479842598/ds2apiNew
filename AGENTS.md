# AGENTS.md

These rules apply to all agent-made changes in this repository.

## PR Gate

- Before opening or updating a PR, run the same local gates as `.github/workflows/quality-gates.yml`.
- Required commands:
  - `./scripts/lint.sh`
  - `./tests/scripts/check-refactor-line-gate.sh`
  - `./tests/scripts/run-unit-all.sh`
  - `npm run build --prefix webui`
- Running on Windows:
  - The shell scripts require Git Bash (Git for Windows).
  - If Git Bash is installed but `bash` is not on `PATH`, invoke the scripts from PowerShell with the full path to `bash.exe`, for example:
    ```powershell
    & "C:\Program Files\Git\bin\bash.exe" -c "./scripts/lint.sh"
    & "C:\Program Files\Git\bin\bash.exe" -c "./tests/scripts/check-refactor-line-gate.sh"
    & "C:\Program Files\Git\bin\bash.exe" -c "./tests/scripts/run-unit-all.sh"
    npm run build --prefix webui
    ```

## Go Lint Rules

- Run `gofmt -w` on every changed Go file before commit or push.
- Do not ignore error returns from I/O-style cleanup calls such as `Close`, `Flush`, `Sync`, or similar methods.
- If a cleanup error cannot be returned, log it explicitly.

## Change Scope

- Keep changes additive and tightly scoped to the requested feature or bugfix.
- Do not mix unrelated refactors into feature PRs unless they are required to make the change pass gates.

## Protocol Adapter Boundary

- Do not let OpenAI Chat, OpenAI Responses, Claude, Gemini, or other interface protocol formatting own shared business behavior.
- Normalize protocol-specific request shapes into the project standard request/turn model first, run shared business logic in one place, then render back to the target protocol at the boundary.
- Business logic that must stay globally consistent includes empty-output retry, thinking/reasoning handling, tool-call detection and policy, usage accounting, current-input-file injection, history persistence, file/reference handling, and completion payload assembly.
- If a behavior must differ by protocol, keep the difference as an explicit adapter/rendering concern and document why it cannot live in the shared normalized path.
- When adding a new feature, also verify that the Vercel path is wired into it.

## Documentation Sync

- When business logic or user-visible behavior changes, update the corresponding documentation in the same change.
- `docs/prompt-compatibility.md` is the source-of-truth document for the “API -> pure-text web-chat context” compatibility flow.
- If a change affects message normalization, tool prompt injection, prompt-visible tool history, file/reference handling, history split, or completion payload assembly, update `docs/prompt-compatibility.md` in the same change.

---

# AGENTS — 本项目用 lrnev 治理

本项目使用 **lrnev** 治理（`.lrnev/` 工作区在项目根目录），全局执行规范见 `~/.agents/AGENTS.md`。

## 每次会话开始（尤其要改代码时）
1. 先调 `lrnev_guide` 了解工作流（或直接看下面的速查）。
2. 要改且不确定进度时，先调 `project_status` 接手当前进度；`governance_map` 看 scene→spec 全景。
3. 需要动手改代码/推进治理时，确认有对应 Spec/Task 并 `task_update(in_progress)`，完成再 `task_update(completed)`。
4. 纯只读/答问题不需要走任何治理流程。

## lrnev 规则（速查）
1. **先分清"只读"还是"要改"**：纯查代码、定位、解释、回答问题这类不改任何文件的事，直接做——不用先 `project_status`，也不用开 spec。下面的流程只在"要动手改代码或推进治理(建/改 spec、task)"时才走。
2. **要改且不确定进度时**，先调 `project_status` 接手现状，别凭记忆直接改代码。也可调 `governance_map` 看 scene→spec 全景压缩视图，快速定位上下文。
3. **该不该开 spec、开在哪**，自己判断、别对着清单匹配。按从便宜到贵判断：
   - ① 写不出独立"WHEN…THEN"验收的小改动（改文档/排版/注释/小重构/调参数/答问题）→ 直接做，不开 spec/task
   - ② 给已完成特性加东西、能落到某现有 spec → 先 `context_search` 找到它，`task_create` 落位（不新开 spec，scene 沿用；completed spec 可 `spec_update` 回退到 in-progress）
   - ③ 真正独立可交付的新特性才开 spec——(a)能写出一条有意义的"WHEN…THEN"验收吗？(b)是可独立交付的特性吗？两个都"是"才开 spec。优先归入已有匹配业务域 scene；只有用户明确确认、或上下文非常清楚这是会承载多个 spec 的新业务域，才 `scene_create`
   - ④ 确实无稳定业务域、又是零散小型独立特性，才落 00-default（兜底，不是默认堆放处）
   - scene / 00-default 是结构决策、事后难迁：该新建 scene 还是落 00-default 拿不准时就问我，别默认。
4. **踩坑→`error_record`，技术决策→`adr_create`，约定→`memory_save`**；都不沾的小事直接做。
5. **多特性需求**先按拆分标尺判断单/多 Spec（可用 `assess_goal` 辅助），别把多个特性塞进一个 Spec。
6. **改代码前**确认对应 task 已 `task_update(in_progress)`，完成后 `task_update(completed)`；纯只读/答问题不涉及 task。
7. **不清楚怎么用**就调 `lrnev_guide`。
8. **Gate 门禁**：spec 创建/就绪/完成各有对应门禁（creation / ready / completion），用 `spec_gate_check` 传 `gate` 参数验证是否达标。
9. 想看治理欠债、收口缺口或 validates 覆盖率，可调 `lrnev_report`；它是给人看的只读体检，不是必走 gate。
10. **工作区结构异常**：可先调 `lrnev_doctor` 做健康诊断。

## 记忆
- 记忆统一由 lrnev 管理，落在本项目 `.lrnev/memory/`：约定→`memory_save`，踩坑→`error_record`，技术决策→`adr_create`；任务开始前可 `memory_search` 检索。
