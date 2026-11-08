import { Choice } from "./Choice";
import type {
  ReasoningCapabilities,
  ReasoningSelection,
} from "@/lib/api/types";

export function reasoningSelection(
  value: string,
): ReasoningSelection | undefined {
  if (value === "inherit") return undefined;
  if (value.startsWith("effort:")) return { effort: value.slice(7) };
  return { mode: value };
}

export function ReasoningChoice({
  caps,
  protocol,
  value,
  onChange,
  inherit = true,
  label = "本轮思考",
  onConfigure,
}: {
  caps?: ReasoningCapabilities;
  protocol?: string;
  value: string;
  onChange: (value: string) => void;
  inherit?: boolean;
  label?: string;
  onConfigure?: () => void;
}) {
  if (!caps?.control || caps.control === "none") {
    return inherit ? (
      <button
        type="button"
        className="choice"
        aria-label="配置当前模型的思考能力"
        title="当前模型未声明思考控制；在模型与 Key → 模型能力中配置，不会自动发送思考参数。"
        disabled={!onConfigure}
        onClick={onConfigure}
      >
        思考：未声明
      </button>
    ) : null;
  }
  const efforts = (caps.efforts || "").split(",").filter(Boolean);
  const options: [string, string][] = [
    ...(inherit ? [["inherit", "继承模型设置"] as [string, string]] : []),
    ["default", "Provider 默认"],
    ...(caps.control === "deepseek" || efforts.includes("none")
      ? [["disabled", "关闭思考"] as [string, string]]
      : []),
    ...(caps.control === "deepseek" && protocol !== "responses"
      ? [["enabled", "开启思考（默认强度）"] as [string, string]]
      : []),
    ...efforts
      .filter((effort) => effort !== "none")
      .map(
        (effort) =>
          ["effort:" + effort, "思考 · " + effort] as [string, string],
      ),
  ];
  return (
    <Choice
      label={label}
      prefix={
        inherit && (value === "inherit" || value === "default") ? "思考：" : ""
      }
      value={
        options.some(([key]) => key === value)
          ? value
          : inherit
            ? "inherit"
            : "default"
      }
      options={options}
      onChange={onChange}
    />
  );
}

export function reasoningValue(value?: ReasoningSelection) {
  return value?.effort ? "effort:" + value.effort : value?.mode || "default";
}
