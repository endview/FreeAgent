import { Choice } from "./Choice";
import type {
  ReasoningCapabilities,
  ReasoningModelInfo,
} from "@/lib/api/types";
import { reasoningOptions } from "@/lib/reasoning";
export { reasoningSelection, reasoningValue } from "@/lib/reasoning";

export function ReasoningChoice({
  caps,
  protocol,
  info,
  value,
  onChange,
  inherit = true,
  label = "本轮思考",
  onConfigure,
}: {
  caps?: ReasoningCapabilities;
  protocol?: string;
  info?: ReasoningModelInfo | null;
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
  const options = reasoningOptions(caps, protocol, info, inherit);
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
