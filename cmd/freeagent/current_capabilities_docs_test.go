package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/runtimefacts"
)

func TestCurrentCapabilityInventoryIsAuthoritativeAndSeparateFromHistory(
	t *testing.T,
) {
	root := filepath.Clean(filepath.Join("..", ".."))
	inventory := readCapabilityDocument(t, root, "docs", "CURRENT_CAPABILITIES.md")
	generatedCanonical := readCapabilityDocument(
		t,
		root,
		"docs",
		"generated",
		"runtime-facts.json",
	)
	var generated runtimefacts.Facts
	if err := json.Unmarshal([]byte(generatedCanonical), &generated); err != nil {
		t.Fatalf("restore generated runtime facts: %v", err)
	}
	if generated.SchemaVersion != runtimefacts.SchemaVersion {
		t.Fatalf("generated runtime facts schema=%q", generated.SchemaVersion)
	}
	const collaborationStatus = "W5_F1_COLLABORATION_STABILITY_ACCEPTED_DEVELOPMENT_SLICE / " +
		"W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W6_NEXT"
	const currentNextFunctionEntry = "W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT"
	const currentNextStageEntry = "P5_BETA_GATE"
	const w6U6CurrentStatus = "W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE"
	const historicalW6U5NextEntry = "W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT"
	const historicalW6U3NextEntry = "W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT"
	const historicalW6U4NextEntry = "W6_4_MODULES_CONFIGURATION_UI_NEXT"
	const historicalW6U2MutationWiringNextEntry = "W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT"
	const historicalW6U2ReceiptSchemaNextEntry = "W6_2_DURABLE_RECEIPT_SCHEMA_NEXT"
	const historicalW6U2AuditNextEntry = "W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT"
	const historicalW6U1NextEntry = "W6_1_APPLICATION_SERVICES_READ_API_NEXT"
	const historicalW6U0NextEntry = "W6_0_CONTROL_API_CONTRACT_NEXT"
	const historicalW2U4NextEntry = "W2_U4_OPERATOR_APPLY_NEXT"
	const w2DLocalAssemblyStatus = "W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE"
	const w2E5BCurrentStatus = "W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE"
	const w2R1CurrentStatus = "W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE"
	const w2R2CurrentStatus = "W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE"
	const w2R3CurrentStatus = "W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE"
	const w2U0CurrentStatus = "W2_U0_SIGNING_SOURCE_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE"
	const w2U1CurrentStatus = "W2_U1_SIGNING_SOURCE_POLICY_ACCEPTED_DEVELOPMENT_SLICE"
	const w2U2CurrentStatus = "W2_U2_DISCOVERY_SNAPSHOT_ACCEPTED_DEVELOPMENT_SLICE"
	const w2U3CurrentStatus = "W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE"
	const w2U4CurrentStatus = "W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE"
	const w6U0CurrentStatus = "W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE"
	const w6U1CurrentStatus = "W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE"
	const w6U2AuditCurrentStatus = "W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE"
	const w6U2ConfirmationCurrentStatus = "W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE"
	const w6U2ReceiptSchemaCurrentStatus = "W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE"
	const w6U2MutationWiringCurrentStatus = "W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE"
	const w6U4CurrentStatus = "W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE"
	const w6U5CurrentStatus = "W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE"
	if !strings.Contains(inventory, "CURRENT_CAPABILITY_INVENTORY_V1") ||
		!strings.Contains(inventory, "HISTORICAL_BASELINE_NON_NORMATIVE") {
		t.Fatal("current capability inventory lacks its authority or history boundary")
	}
	if !strings.Contains(inventory, "既有 W5 路线状态：`"+collaborationStatus+"`") {
		t.Fatal("current capability inventory lost the historical accepted W5-F1/W5 status")
	}
	if !strings.Contains(inventory, "当前下一入口为 `"+currentNextStageEntry+"`") {
		t.Fatal("current capability inventory lost the exact next-entry declaration")
	}
	if !strings.Contains(inventory, "当前收口：`"+w6U5CurrentStatus+" / "+w6U6CurrentStatus) {
		t.Fatal("current capability inventory lost the exact W6-5/W6-6 declaration")
	}
	if !strings.Contains(inventory, currentNextFunctionEntry) ||
		!strings.Contains(inventory, w6U6CurrentStatus) ||
		!strings.Contains(inventory, historicalW6U5NextEntry) ||
		!strings.Contains(inventory, w6U5CurrentStatus) ||
		!strings.Contains(inventory, historicalW6U4NextEntry) ||
		!strings.Contains(inventory, w6U4CurrentStatus) ||
		!strings.Contains(inventory, historicalW6U3NextEntry) ||
		!strings.Contains(inventory, historicalW6U2MutationWiringNextEntry) ||
		!strings.Contains(inventory, historicalW6U2ReceiptSchemaNextEntry) {
		t.Fatal("current capability inventory lost the current W6-5 or historical W6-2/W6-3/W6-4 marker")
	}
	if !strings.Contains(inventory, historicalW6U2AuditNextEntry) ||
		!strings.Contains(inventory, w6U2AuditCurrentStatus) ||
		!strings.Contains(inventory, w6U2ConfirmationCurrentStatus) ||
		!strings.Contains(inventory, w6U2ReceiptSchemaCurrentStatus) {
		t.Fatal("current capability inventory lost the W6-2 audit/confirmation history boundary")
	}
	if !strings.Contains(inventory, historicalW6U1NextEntry) {
		t.Fatal("current capability inventory lost the explicitly historical W6-1 next marker")
	}
	if !strings.Contains(inventory, historicalW6U0NextEntry) {
		t.Fatal("current capability inventory lost the explicitly historical W6-0 next marker")
	}
	if !strings.Contains(inventory, historicalW2U4NextEntry) {
		t.Fatal("current capability inventory lost the explicitly historical W2-U4 next marker")
	}
	if !strings.Contains(inventory, "权威能力清单共 42 项") ||
		!strings.Contains(inventory, "不是历史发布 Capability Matrix 的 49 项") {
		t.Fatal("current capability inventory lost the current-row versus historical 49-item boundary")
	}
	if !strings.Contains(inventory, w2R3CurrentStatus) {
		t.Fatal("current capability inventory lost the accepted W2-R3 status")
	}
	if !strings.Contains(inventory, w2U0CurrentStatus) {
		t.Fatal("current capability inventory lost the accepted W2-U0 status")
	}
	if !strings.Contains(inventory, w2U1CurrentStatus) {
		t.Fatal("current capability inventory lost the accepted W2-U1 status")
	}
	if !strings.Contains(inventory, w2U2CurrentStatus) {
		t.Fatal("current capability inventory lost the accepted W2-U2 status")
	}
	if !strings.Contains(inventory, w2U3CurrentStatus) {
		t.Fatal("current capability inventory lost the accepted W2-U3 status")
	}
	if !strings.Contains(inventory, w2U4CurrentStatus) {
		t.Fatal("current capability inventory lost the accepted W2-U4 status")
	}
	if !strings.Contains(inventory, w2DLocalAssemblyStatus) {
		t.Fatal("current capability inventory lost the accepted narrow W2-D predecessor status")
	}

	expected := make(map[string]string, generated.Capabilities.Count)
	for _, capability := range generated.Capabilities.Entries {
		if _, duplicate := expected[capability.ID]; duplicate {
			t.Fatalf("duplicate generated capability ID %q", capability.ID)
		}
		expected[capability.ID] = capability.Status
	}
	if len(expected) != generated.Capabilities.Count {
		t.Fatalf(
			"generated capability entries=%d count=%d",
			len(expected),
			generated.Capabilities.Count,
		)
	}
	rowPattern := regexp.MustCompile(
		`(?m)^\| ` + "`" + `([a-z0-9][a-z0-9.-]+)` + "`" +
			` \| [^|]+ \| ` + "`" +
			`(accepted|experimental|unverified|planned)` + "`" +
			` \| [^|]+ \|$`,
	)
	observed := make(map[string]string, len(expected))
	for _, match := range rowPattern.FindAllStringSubmatch(inventory, -1) {
		if _, duplicate := observed[match[1]]; duplicate {
			t.Fatalf("duplicate current capability ID %q", match[1])
		}
		observed[match[1]] = match[2]
	}
	if len(observed) != len(expected) {
		t.Fatalf("current capability rows = %d, want %d", len(observed), len(expected))
	}
	for id, status := range expected {
		if observed[id] != status {
			t.Fatalf("current capability %q status = %q, want %q", id, observed[id], status)
		}
	}
	for _, evidence := range []string{
		w6U5CurrentStatus,
		currentNextFunctionEntry,
		currentNextStageEntry,
		w6U6CurrentStatus,
		"`module-artifact-ingress`",
		"`--enable-module-artifact-ingress`",
		"`--source-root`",
		"`--artifact-root`",
		"Store-owned current Snapshot",
		"unsigned `LOCAL_DIRECTORY + DENY`",
		"不能提供 package path、URL 或 signature",
		"filesystem durable-first",
		"digest-addressed no-replace",
		"`module_artifacts`",
		"`module_artifact_admissions`",
		"Installation 与 ingress 的 artifact digest 去重合并",
		"Source 已移除且零 Installation",
		"43 tables / 25 explicit indexes / 64 triggers",
		"47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d",
		"150,301 bytes",
		"6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W6-5 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		"module-upgrade-review-server-owned",
		"module-upgrade-decide-server-owned",
		"ArtifactAdmissionID",
		"OperatorPrincipalID",
		"ReviewRequestDigest",
		"content identity exact retry",
		"跨 tenant、source/head stale、物理篡改",
		"不自动 Install、Activate、Bind、Grant、Apply、Execute",
		"不调用 Provider",
		"UserVersion 2",
		"42 tables / 26 explicit indexes / 64 triggers",
		"d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e",
		"0002_server_owned_review.sql",
		"7,173 bytes",
		"3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W6.6 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w2U0CurrentStatus,
		"七份纯合同",
		"Ed25519",
		"opaque Version",
		"跨刷新 review-key",
		"不联网、不改 24 表 Store",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W2-U0 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w2U1CurrentStatus,
		"governed mode 只接受 `LOCAL_DIRECTORY + DENY`",
		"本次调用冻结的 immutable snapshot",
		"unsigned Discovery Snapshot",
		"不创建 reservation、grant、stage、Store",
		"手工 exact-grant",
		"九个 handler",
		"U1 本身仍不持久化 Source/Discovery",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W2-U1 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w2U2CurrentStatus,
		"observation-only",
		"module-source-register",
		"module-source-refresh",
		"module-publisher-key-revoke",
		"root/index.json",
		"exact HTTPS origin allowlist",
		"Source I/O 前后",
		"全局不可逆 Publisher Key revocation",
		"不生成 Candidate、Decision 或 Apply",
		"Backup/Verify/Restore 零网络",
		"29 表",
		"6d2bded477e7b1d2c755bf47f5b41f63f1b2f7b568fd72496fc43fbceac3a5f1",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W2-U2 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w2U3CurrentStatus,
		"module-upgrade-review",
		"module-upgrade-decide",
		"--enable-module-upgrade-review",
		"EXACT_VERSION_CHANGE",
		"local unpacked artifact",
		"detached Signature",
		"不读取 Index `PackagePath`",
		"target Manifest content evidence",
		"WOULD_APPLY | CONFLICT | UNSUPPORTED",
		"digest-only required grants",
		"--confirm-tenant-wide-reject",
		"{tenant_id, review_key}",
		"系统不读取 Index `PackagePath`、不联网下载",
		"9 条 generic/Model policy 与 2 条 Document Insight reserved",
		"唯一 pure policy/assessor",
		"未复制第二张",
		historicalW2U4NextEntry,
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W2-U3 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w2U4CurrentStatus,
		"module.upgrade-apply-v1",
		"module-upgrade-apply",
		"--enable-module-upgrade-apply",
		"exact Tenant/Review/APPROVE Decision",
		"LOCAL_DIRECTORY + DENY",
		"显式 `--source-root`",
		"不读取 Index `PackagePath`",
		"全部 grant flags 显式出现且为空",
		"PROFILE `context.provide/v1`",
		"DECLARATIVE `static/v1`",
		"`TRUSTED_INSTRUCTION`",
		"deny-all Authority",
		"历史 U1 验证→current Store 重验→同一 exact plan→current U1 验证→pre-stage",
		"原 ordinal 原位替换",
		"其他 Binding 字节与顺序不变",
		"旧 Run 继续冻结旧 Provider，新 Run 使用 target",
		"Model、Channel、Action、`SINGLE`、shared-current 与 fanout 失败关闭",
		"复用同一 evaluator",
		"不虚构独立 dry-run admission",
		"相邻 current exact retry",
		"不建立 durable receipt",
		"后续 publication 后返回 `POINTER_CONFLICT`",
		"UNKNOWN 原样保留且禁止重放",
		"Backup、Pure Chat 零访问与 32 表 Store 不变",
		historicalW6U0NextEntry,
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W2-U4 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		"集中纵链",
		"逐个 Catalog Entry",
		"unbound `module-inspect`",
		"Windows 全仓 188.9 秒",
		"WSL2 ext4 有效 START/COMPLETE 全仓 128.1 秒",
		"License 35/55",
		"`PUBLIC_TREE_PASS`",
		"accepted 只限该窄本地 v1",
		"Operator 入口与",
		"Module Conformance 继续 `experimental`",
		"广义通用装配、W6/W7 与 Beta 继续 `planned`",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W2-D inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		"必须以 exact pair 同时声明 `model.generate/v2` Require",
		"窄 `knowledge.read` grant",
		"只证明 governed Knowledge 的单 Port Require/grant",
		"30 个仓内 Go package 用时 221.9 秒",
		"1 个 external compatibility package",
		"License（35 个 Go dependency/57 个",
		"distributed asset）",
		"未调用真实 API",
		"未新增 Schema、表、",
		"Runtime、Store、Loop 或 Gateway",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W2-E5-A inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w2E5BCurrentStatus,
		"`freeagent.builtin.document-insight@2.0.0`",
		"9cf2e60f4b6d30cfd93ea93245f4a6eadbd4f365b93f06b7decb389c4f4d4bfa",
		"`freeagent.adapter.document-insight/v1`",
		"Context→Action 两步 Apply",
		"同一 Run 首个 Model Compilation 同时冻结一条真实",
		"`ActionResultReservation`",
		"2 个 Model Attempt 与 1 个 Action Attempt",
		"完整相同 `ActivatedModuleRef`",
		"exact Chat retry 零新增 Run/Attempt",
		"Backup→verify→restore",
		"Context-first Disable",
		"Context PortPlan 同时保留既有",
		"`context.basic`",
		"30 个仓内 Go package 的全仓墙钟为",
		"1 个 external compatibility package",
		"Docs（38 份 Markdown）",
		"Capability Matrix（49 项、稳定级 0 项）",
		"Branding 与 PublicTree 均通过",
		"35 个 Go dependency / 59 个 distributed asset",
		"未调用真实 API",
		"任意模块组合的通用 multi-Port",
		"Operator Module Apply/Conformance 保持",
		"`experimental`",
		"`module.general-assembly` 保持 `planned`",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W2-E5-B inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w2R1CurrentStatus,
		"`action.provider/v1 + REMOTE/freeagent-action-http/v1`",
		"runtime 默认关闭",
		"exact artifact、HTTPS endpoint 与 SecretRef grant",
		"原 DispatchAttempt 已为 PENDING",
		"不使用 proxy、redirect、keep-alive、HTTP/2、",
		"无法证明结果时只把原 Attempt 置为 UNKNOWN",
		"Provider `FAILED + HTTP 422` 恰好一次 RoundTrip",
		"本切片没有真实公网 HTTPS",
		"`W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE`",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W2-R1 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w2R2CurrentStatus,
		"`action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1`",
		"`--allow-wasm-action-artifact`",
		"runtime 默认关闭",
		"Catalog lazy load",
		"`wazero v1.12.0` interpreter",
		"Core WASM 1.0",
		"module 16 MiB",
		"request 128 KiB",
		"output 32 KiB",
		"initial 最多 32 页",
		"maximum 最多 256 页",
		"65,536 elements",
		"全进程最多 4",
		"最长 5 秒",
		"唯一 Gateway 在原 PENDING Attempt 后",
		"`instruction_metering=UNSUPPORTED`",
		"无 token/cost/price",
		"trap/timeout/ABI/output 确定性 FAILED",
		"事务/崩溃遗留 PENDING 恢复原 Attempt UNKNOWN",
		"禁止 replay/fallback",
		"Backup/Restore 不编译、实例化或执行 guest",
		"不是 OS/container 或生产恶意多租户隔离",
		"`W2_R3_UNTRUSTED_MODULE_ISOLATION`",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W2-R2 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w2R3CurrentStatus,
		"--allow-wasm-runtime-artifact",
		"execution-admission linearization point",
		"STORE_BUSY",
		"Effect=none",
		"InstanceID",
		"ActivationRevision",
		"OS/container",
		historicalW6U0NextEntry,
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W2-R3 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w6U0CurrentStatus,
		"control.api-contract-v1",
		"control-session/v1",
		"control-scope/v1",
		"control-view-snapshot/v1",
		"control-operation-request/v1",
		"control-operation-receipt/v1",
		"control-event-cursor/v1",
		"freeagent.control-view-snapshot/v1",
		"Core `control-snapshot/v1`",
		"`DRY_RUN/MUTATE`",
		"动态 request ID",
		"UNKNOWN 仅 exact receipt",
		"没有创建网络 listener、HTTP handler、在线 session、SSE 或 UI",
		"不新增 Schema 或 receipt table",
		"39 份 Markdown",
		"37 个源码 package",
		"35 个 production dependency-closure package",
		"FAC1 Store 仍为 32 表",
		historicalW6U1NextEntry,
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W6-0 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w6U1CurrentStatus,
		historicalW6U2AuditNextEntry,
		"control-plane.online",
		"`--enable-control` 默认 false",
		"`tcp4 127.0.0.1:0`",
		"owner-only",
		"process-local",
		"`GET /control/api/v1/modules`",
		"detail",
		"`POST /control/api/v1/modules/disable/dry-run`",
		"effect-free",
		"`DRY_RUN` receipt",
		"GET 缺失或错误 CSRF 返回 401",
		"没有 Control mutation",
		"durable receipt",
		"SSE",
		"UI",
		"后台 worker",
		"39 份 Markdown",
		"44 个源码 package",
		"44 个 production dependency-closure package",
		"FAC1 Store 仍为 32 表",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W6-1 inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w6U2AuditCurrentStatus,
		w6U2ConfirmationCurrentStatus,
		historicalW6U2ReceiptSchemaNextEntry,
		"control-confirmation-statement/v1",
		"operation_evaluation_digest",
		"control-module-disable-evaluation/v1",
		"TENANT/PROFILE",
		"`context.provide/v1`",
		"`OPTIONAL`",
		"`DECLARATIVE`",
		"trusted-instruction config",
		"deny-all Authority",
		"32 random bytes",
		"TTL 最多 2 分钟且不超过当前 session absolute expiry",
		"全局最多 256",
		"每 session 最多 8",
		"domain-separated proof digest",
		"不进入 Store、Backup 或日志",
		"没有 durable receipt row",
		"mutation route",
		"`UNKNOWN` 不可达",
		"39 份 Markdown",
		"45 个源码 package",
		"44 个 production dependency-closure package",
		"当时 FAC1 Store 为 32 表",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W6-2 confirmation inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w6U2ReceiptSchemaCurrentStatus,
		historicalW6U2MutationWiringNextEntry,
		"control_operation_receipts",
		"module-apply-plan/v1",
		"module-disable-publication-receipt/v1",
		"exact resolver",
		"`NO_CHANGE`",
		"`APPLIED`",
		"没有公开 insert",
		"同一 `BEGIN IMMEDIATE` transaction",
		"只验证现存行",
		"不宣称能发现整行删除",
		"Backup Create/Verify/Restore",
		"39 份 Markdown",
		"47 个源码 package",
		"46 个 production dependency-closure package",
		"当前 FAC1 Store 为 33 表",
		"51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10",
		"67,998 bytes",
		"8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952",
		"没有 mutation route/handler",
		"confirmation endpoint",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W6-2 receipt Schema inventory lacks evidence or boundary %q", evidence)
		}
	}
	for _, evidence := range []string{
		w6U2MutationWiringCurrentStatus,
		historicalW6U3NextEntry,
		"--enable-control",
		"modules/disable/confirmation",
		"/mutate",
		"TENANT/PROFILE",
		"`context.provide/v1`",
		"`OPTIONAL`",
		"`DECLARATIVE static/v1`",
		"trusted-instruction config",
		"deny-all Authority",
		"Origin/session/session-bound CSRF/TENANT Permit",
		"不依赖旧 proof",
		"旧 session identity",
		"最多 2 分钟且不超过当前 session absolute expiry",
		"durable resolver lookup-first",
		"`NO_CHANGE` 与 `APPLIED`",
		"同一 `BEGIN IMMEDIATE` transaction",
		"纯 SQLite `UNKNOWN` 不可达",
		"external completeness anchor",
		"不能发现任意整行 receipt 删除",
		"forced receipt-insert failure",
		"48 个源码 package",
		"48 个 production dependency-closure package",
		"33 表 Store",
		"无其他 mutation、SSE/UI/worker、第二 Store/writer",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W6-2 mutation-wiring inventory lacks evidence or boundary %q", evidence)
		}
	}
	if !strings.Contains(inventory, "仅验收普通单 Agent 单模型 Pure Chat") ||
		!strings.Contains(inventory, "W1_COMPLETE_REAL_DEEPSEEK_50") ||
		!strings.Contains(inventory, "W3_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE") {
		t.Fatal("accepted Conversation capability lost its narrow maturity boundary")
	}
	if !strings.Contains(inventory, "W4_L4_LEARNING_CYCLE_ACCEPTED_DEVELOPMENT_SLICE") ||
		!strings.Contains(inventory, "W4_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE") ||
		!strings.Contains(inventory, "W5_X1_WORKSPACE_TRANSFER_ACCEPTED_DEVELOPMENT_SLICE") ||
		!strings.Contains(inventory, "当前 FAC1 Store 为 33 表") ||
		!strings.Contains(
			inventory,
			"9941c957b0e6a1a1e1770b38cd06605025d12df30067d1271fe8f7b30f2f0518",
		) {
		t.Fatal("current inventory lost a historical accepted status or frozen Store identity")
	}

	for _, evidence := range []string{
		"900 次 claim",
		"第 450 次后重开 Store",
		"最终各 300 次",
		"首次服务不晚于第 3 次",
		"最大服务间隔不超过 3",
		"Decision approve、单槽 repair 与 Decision+Transfer 均由唯一 Scheduler claim",
		"dormant/skipped repair Run 保持 `Attempt=nil` 且零 claim",
		"Specialist 反向完成后仍按 frozen plan 排列",
		"Store reopen 后 canonical bytes 与身份不变",
		"取消后不创建新 Attempt、Transfer payload 或 envelope",
		"可靠 evidence 只收口原 UNKNOWN Attempt",
		"SUCCEEDED 恰好生成一个 RESULT",
		"FAILED 不生成 RESULT",
		"stale revision、替换 Provider、替换 Attempt 和无 evidence 均拒绝",
		"backup→verify→restore→reopen 已通过",
		"同一 Attempt UNKNOWN",
		"Universal Loop 语义重放为 0",
		"root/target 双边 grant、Tenant、Workspace revision、方向、kind/schema/ref/size、48 KiB",
		"Pure Chat 和 ordinary Composite 均为零 Transfer material",
		"Windows 全仓测试、`go vet`、`go mod verify` 与 gofmt 检查均通过",
		"首轮受影响七包 race 中六包通过",
		"CurrentStore 仅暴露 1 秒 TTL 墙钟测试竞态且没有 data race",
		"完整 CurrentStore race 以 exit 0 在 466.241 秒结束",
		"首轮七包命令本身不记作整体 exit 0",
		"F1 Test-0808 canary 为 4/4 HTTP 2xx",
		"exact retry 新 HTTP/Attempt 均为 0",
		"3,149/768/2,381/1,492",
		"2/4 请求命中缓存",
		"24.388695%",
		"0.00538036 CNY",
		"reasoning token、Provider reported cost 与 reconciled cost",
		"X1D 历史样本",
		"项目从未正式部署",
		"`RELEASE_READY`",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("accepted W5-F1 inventory lacks evidence or boundary %q", evidence)
		}
	}
	learningCycleRow := ""
	for _, line := range strings.Split(inventory, "\n") {
		if strings.HasPrefix(line, "| `knowledge.learning-cycle` |") {
			learningCycleRow = line
			break
		}
	}
	for _, boundary := range []string{
		"新 Schedule 默认关闭",
		"exact revision 显式启用",
		"显式 UTC `--observed-at`",
		"`learning-cycle-reconcile` 仅补齐现有 Store 投影",
		"`learning-cycle-report` 只读生成不持久化",
		"没有常驻 Worker、Queue 或后台 daemon",
		"不自动 Apply、安装、激活、绑定或扩权",
	} {
		if !strings.Contains(learningCycleRow, boundary) {
			t.Fatalf("accepted Learning cycle capability lacks boundary %q", boundary)
		}
	}

	for _, reference := range []struct {
		path string
		text string
	}{
		{"README.md", "docs/CURRENT_CAPABILITIES.md"},
		{"docs/PRD.md", "CURRENT_CAPABILITIES.md"},
		{"docs/specs/README.md", "../CURRENT_CAPABILITIES.md"},
	} {
		document := readCapabilityDocument(t, root, filepath.FromSlash(reference.path))
		if !strings.Contains(document, reference.text) {
			t.Fatalf("%s does not reference the current capability inventory", reference.path)
		}
	}

	for _, path := range []string{
		"README.md",
		"docs/PRD.md",
		"docs/CURRENT_CAPABILITIES.md",
	} {
		document := readCapabilityDocument(t, root, filepath.FromSlash(path))
		if !strings.Contains(document, collaborationStatus) {
			t.Fatalf("%s does not publish the authoritative W5-F1/W5 status", path)
		}
	}
	for _, path := range []string{
		"README.md",
		"docs/PRD.md",
		"docs/CURRENT_CAPABILITIES.md",
		"docs/MODULE_DEVELOPMENT_V1.md",
	} {
		document := readCapabilityDocument(t, root, filepath.FromSlash(path))
		if !strings.Contains(document, w2R2CurrentStatus) {
			t.Fatalf("%s does not publish the accepted narrow W2-R2 status", path)
		}
		if !strings.Contains(document, currentNextFunctionEntry) {
			t.Fatalf("%s does not publish the W6-6 server-owned module upgrade review entry", path)
		}
		if !strings.Contains(document, w6U5CurrentStatus) ||
			!strings.Contains(document, historicalW6U5NextEntry) {
			t.Fatalf("%s does not publish W6-5 current status and historical entry", path)
		}
		if !strings.Contains(document, w6U2MutationWiringCurrentStatus) ||
			!strings.Contains(document, historicalW6U2MutationWiringNextEntry) {
			t.Fatalf("%s does not preserve mutation-wiring history and current status", path)
		}
		if !strings.Contains(document, historicalW6U2ReceiptSchemaNextEntry) ||
			!strings.Contains(document, w6U2ReceiptSchemaCurrentStatus) {
			t.Fatalf("%s does not preserve receipt-Schema history and current status", path)
		}
		if !strings.Contains(document, historicalW6U2AuditNextEntry) ||
			!strings.Contains(document, w6U2AuditCurrentStatus) ||
			!strings.Contains(document, w6U2ConfirmationCurrentStatus) {
			t.Fatalf("%s does not preserve the W6-2 audit and current confirmation boundary", path)
		}
		if !strings.Contains(document, w6U1CurrentStatus) {
			t.Fatalf("%s does not publish the accepted W6-1 application-services status", path)
		}
		if !strings.Contains(document, w6U0CurrentStatus) {
			t.Fatalf("%s does not publish the accepted W6-0 contract status", path)
		}
		if !strings.Contains(document, historicalW6U1NextEntry) {
			t.Fatalf("%s lost the historical W6-0 to W6-1 marker", path)
		}
		if !strings.Contains(document, w2U2CurrentStatus) {
			t.Fatalf("%s does not publish the accepted narrow W2-U2 status", path)
		}
		if !strings.Contains(document, w2U3CurrentStatus) {
			t.Fatalf("%s does not publish the accepted narrow W2-U3 status", path)
		}
		if !strings.Contains(document, w2U4CurrentStatus) {
			t.Fatalf("%s does not publish the accepted narrow W2-U4 status", path)
		}
	}
	for _, path := range []string{
		"README.md",
		"SECURITY.md",
		"docs/PRD.md",
		"docs/CURRENT_CAPABILITIES.md",
		"docs/MODULE_DEVELOPMENT_V1.md",
		"docs/CUTOVER_ACCEPTANCE.md",
		"docs/specs/CORE_RUNTIME_V1.md",
		"docs/specs/CURRENT_STORE_V1.md",
	} {
		document := readCapabilityDocument(t, root, filepath.FromSlash(path))
		if !strings.Contains(document, w2U2CurrentStatus) {
			t.Fatalf("%s does not publish the accepted W2-U2 status", path)
		}
		if !strings.Contains(document, w2U3CurrentStatus) {
			t.Fatalf("%s does not publish the accepted W2-U3 status", path)
		}
		if !strings.Contains(document, w2U4CurrentStatus) {
			t.Fatalf("%s does not publish the accepted W2-U4 status", path)
		}
		if !strings.Contains(document, currentNextFunctionEntry) {
			t.Fatalf("%s does not publish the W6-6 server-owned module upgrade review entry", path)
		}
		if !strings.Contains(document, w6U5CurrentStatus) ||
			!strings.Contains(document, historicalW6U5NextEntry) ||
			!strings.Contains(document, historicalW6U3NextEntry) ||
			!strings.Contains(document, historicalW6U4NextEntry) ||
			!strings.Contains(document, w6U4CurrentStatus) {
			t.Fatalf("%s does not preserve W6-3/W6-4 history and W6-5 current status", path)
		}
		if !strings.Contains(document, w6U2MutationWiringCurrentStatus) ||
			!strings.Contains(document, historicalW6U2MutationWiringNextEntry) {
			t.Fatalf("%s does not preserve mutation-wiring history and current status", path)
		}
		if !strings.Contains(document, historicalW6U2ReceiptSchemaNextEntry) ||
			!strings.Contains(document, w6U2ReceiptSchemaCurrentStatus) {
			t.Fatalf("%s does not preserve receipt-Schema history and current status", path)
		}
		if !strings.Contains(document, historicalW6U2AuditNextEntry) ||
			!strings.Contains(document, w6U2AuditCurrentStatus) ||
			!strings.Contains(document, w6U2ConfirmationCurrentStatus) {
			t.Fatalf("%s does not preserve the W6-2 audit and current confirmation boundary", path)
		}
		if !strings.Contains(document, w6U1CurrentStatus) {
			t.Fatalf("%s does not publish the accepted W6-1 application-services status", path)
		}
		if !strings.Contains(document, w6U0CurrentStatus) {
			t.Fatalf("%s does not publish the accepted W6-0 contract status", path)
		}
		if !strings.Contains(document, historicalW6U1NextEntry) {
			t.Fatalf("%s lost the historical W6-0 to W6-1 marker", path)
		}
		if !strings.Contains(document, historicalW6U0NextEntry) {
			t.Fatalf("%s lost the historical U4 to W6-0 marker", path)
		}
	}
	controlSpec := readCapabilityDocument(t, root, "docs", "specs", "CONTROL_API_V1.md")
	for _, evidence := range []string{
		w6U0CurrentStatus,
		historicalW6U1NextEntry,
		w6U1CurrentStatus,
		historicalW6U2AuditNextEntry,
		w6U2AuditCurrentStatus,
		w6U2ConfirmationCurrentStatus,
		historicalW6U2ReceiptSchemaNextEntry,
		w6U2ReceiptSchemaCurrentStatus,
		historicalW6U2MutationWiringNextEntry,
		w6U2MutationWiringCurrentStatus,
		historicalW6U3NextEntry,
		historicalW6U4NextEntry,
		w6U4CurrentStatus,
		historicalW6U5NextEntry,
		w6U5CurrentStatus,
		currentNextFunctionEntry,
		"control-confirmation-statement/v1",
		"operation_evaluation_digest",
		"control-module-disable-evaluation/v1",
		"32 random bytes",
		"TTL 最多 2 分钟且不超过当前 session absolute expiry",
		"全局最多 256",
		"每 session 最多 8",
		"control-view-snapshot/v1",
		"control-snapshot/v1",
		"DRY_RUN",
		"MUTATE",
		"UNKNOWN",
		"tcp4 127.0.0.1:0",
		"Modules GET 与非安全方法都要求 exact session + session-bound",
		"GET 缺失/错误 CSRF 返回 401",
		"MODULE_DISABLE",
		"effect-free",
		"process-local",
		"CONTROL_MUTATION = NONE",
		"DURABLE_RECEIPT_TABLE = NONE",
		"SOURCE_PACKAGES = 44",
		"SOURCE_PACKAGES = 45",
		"PRODUCTION_DEPENDENCY_CLOSURE = 44",
		"STORE_SCHEMA = UNCHANGED_32_TABLES",
		"STORE_SCHEMA = 33_TABLES",
		"PUBLIC_COMMIT = NO_CHANGE_ONLY",
		"APPLIED_PUBLIC_INSERT = NONE",
		"SCHEMA_FINGERPRINT = 51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10",
		"MIGRATION_BYTES = 67998",
		"MIGRATION_SHA256 = 8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952",
		"CONTROL_HTTP_ARTIFACT_INGRESS = NONE",
		"INGRESS_ENTRY = TRUSTED_LOCAL_OPERATOR_CLI_DEFAULT_OFF",
		"CALLER_PACKAGE_PATH_URL_SIGNATURE_UPLOAD_UI = NONE",
		"SOURCE_POLICY = UNSIGNED_LOCAL_DIRECTORY_DENY_ONLY",
		"FILESYSTEM_PUBLICATION = DURABLE_FIRST_CONTENT_ADDRESSED_NO_REPLACE",
		"STORE_COMMIT = IMMUTABLE_ARTIFACT_AND_APPEND_ONLY_ADMISSION_SAME_BEGIN_IMMEDIATE",
		"ARTIFACT_AUTHORITY = NONE",
		"BACKUP_ARTIFACT_SET = INSTALLATION_UNION_INGRESS",
		"STORE_SCHEMA = 43_TABLES_25_EXPLICIT_INDEXES_64_TRIGGERS",
		"SCHEMA_FINGERPRINT = 47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d",
		"MIGRATION_BYTES = 150301",
		"MIGRATION_SHA256 = 6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86",
	} {
		if !strings.Contains(controlSpec, evidence) {
			t.Fatalf("CONTROL_API_V1 lacks accepted Control contract evidence %q", evidence)
		}
	}
	specIndex := readCapabilityDocument(t, root, "docs", "specs", "README.md")
	if !strings.Contains(specIndex, "CONTROL_API_V1") ||
		!strings.Contains(specIndex, "CONTROL_API_V1.md") ||
		!strings.Contains(specIndex, w6U1CurrentStatus) ||
		!strings.Contains(specIndex, w6U2ConfirmationCurrentStatus) ||
		!strings.Contains(specIndex, historicalW6U2ReceiptSchemaNextEntry) ||
		!strings.Contains(specIndex, w6U2ReceiptSchemaCurrentStatus) ||
		!strings.Contains(specIndex, w6U2MutationWiringCurrentStatus) ||
		!strings.Contains(specIndex, historicalW6U3NextEntry) ||
		!strings.Contains(specIndex, w6U4CurrentStatus) ||
		!strings.Contains(specIndex, historicalW6U5NextEntry) ||
		!strings.Contains(specIndex, w6U5CurrentStatus) ||
		!strings.Contains(specIndex, currentNextFunctionEntry) {
		t.Fatal("current specification index does not publish CONTROL_API_V1")
	}
	for _, evidence := range []string{
		"删除前的 FAC1 Store 为 43 表",
		"47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d",
		"150,301 bytes",
		"6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86",
		"历史 41 表",
		"87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1",
		"143,588 bytes",
		"5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22",
		"当前 FAC1 Store 为 33 表",
		"51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10",
		"67,998 bytes",
		"8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952",
		"历史 32 表",
		"37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd",
		"57,652 bytes",
		"e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d",
		"历史 29 表 identity",
		"6d2bded477e7b1d2c755bf47f5b41f63f1b2f7b568fd72496fc43fbceac3a5f1",
		"49,972 bytes",
		"d2bcc27bff2e17a058165f7c544b0c97cd1c99efca3264ab401631477611263e",
	} {
		if !strings.Contains(inventory, evidence) {
			t.Fatalf("current inventory lacks current or historical Store identity %q", evidence)
		}
	}
	for _, path := range []string{
		"README.md",
		"docs/PRD.md",
		"docs/CURRENT_CAPABILITIES.md",
		"docs/MODULE_DEVELOPMENT_V1.md",
		"docs/CUTOVER_ACCEPTANCE.md",
		"docs/specs/CORE_RUNTIME_V1.md",
		"docs/specs/CURRENT_STORE_V1.md",
	} {
		document := readCapabilityDocument(t, root, filepath.FromSlash(path))
		if !strings.Contains(document, w2DLocalAssemblyStatus) {
			t.Fatalf("%s does not publish the accepted narrow W2-D status", path)
		}
		if strings.Contains(document, "W2_D_IMPLEMENTED_AWAITING_FULL_GATES") {
			t.Fatalf("%s still publishes the superseded W2-D awaiting status", path)
		}
		if !strings.Contains(document, w2E5BCurrentStatus) {
			t.Fatalf("%s does not publish the accepted narrow W2-E5-B status", path)
		}
		if !strings.Contains(document, w2R1CurrentStatus) {
			t.Fatalf("%s does not publish the accepted narrow W2-R1 status", path)
		}
	}

	historical := readCapabilityDocument(t, root, "docs", "RELEASE_MATURITY.md")
	if !strings.Contains(historical, "HISTORICAL_BASELINE_NON_NORMATIVE") ||
		!strings.Contains(historical, "archival classifications only") {
		t.Fatal("historical capability matrix lost its non-normative boundary")
	}
}

func readCapabilityDocument(t *testing.T, root string, elements ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, elements...)...)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
