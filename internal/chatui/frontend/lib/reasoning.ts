import type {
  ReasoningCapabilities,
  ReasoningModelInfo,
  ReasoningSelection,
} from "./api/types.ts";

export function reasoningSelection(
  value: string,
): ReasoningSelection | undefined {
  if (value === "inherit") return undefined;
  if (value.startsWith("effort:")) return { effort: value.slice(7) };
  return { mode: value };
}

export function reasoningValue(value?: ReasoningSelection) {
  return value?.effort ? "effort:" + value.effort : value?.mode || "default";
}

export function reasoningOptions(
  caps: ReasoningCapabilities,
  protocol = "chat",
  info?: ReasoningModelInfo | null,
  inherit = true,
): [string, string][] {
  const efforts = [...new Set((caps.efforts || "").split(",").filter(Boolean))];
  const known =
    info && info.control === caps.control ? info.efforts.split(",") : [];
  const providerDefault = !info
    ? "Provider 默认"
    : info.default_mode === "disabled"
      ? "Provider 默认（思考关闭）"
      : `Provider 默认（思考开启${info.default_effort ? " · " + info.default_effort : ""}）`;
  const nativeToggle =
    caps.control === "deepseek" || caps.control === "anthropic";
  return [
    ...(inherit ? [["inherit", "继承模型设置"] as [string, string]] : []),
    ["default", providerDefault],
    ...(!info?.required && (nativeToggle || efforts.includes("none"))
      ? [["disabled", "关闭思考"] as [string, string]]
      : []),
    ...(nativeToggle && protocol !== "responses"
      ? [
          [
            "enabled",
            caps.control === "anthropic"
              ? "自适应思考（默认强度）"
              : "开启思考（默认强度）",
          ] as [string, string],
        ]
      : []),
    ...efforts
      .filter((effort) => effort !== "none")
      .map(
        (effort) =>
          [
            "effort:" + effort,
            "思考 · " +
              effort +
              (known.includes(effort) ? "" : "（未经官方核对）"),
          ] as [string, string],
      ),
  ];
}
