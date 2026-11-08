import { Select, Switch } from "radix-ui";
import { Check, ChevronDown } from "lucide-react";
export function Choice({
  value,
  onChange,
  label,
  options,
  disabled = false,
  prefix = "",
}: {
  value: string;
  onChange: (value: string) => void;
  label: string;
  options: [string, string][];
  disabled?: boolean;
  prefix?: string;
}) {
  return (
    <Select.Root value={value} onValueChange={onChange} disabled={disabled}>
      <Select.Trigger aria-label={label} className="choice">
        <Select.Value>
          {prefix
            ? prefix + (options.find(([id]) => id === value)?.[1] || value)
            : undefined}
        </Select.Value>
        <Select.Icon>
          <ChevronDown size={14} />
        </Select.Icon>
      </Select.Trigger>
      <Select.Portal>
        <Select.Content
          className="choice-menu"
          position="popper"
          sideOffset={5}
        >
          <Select.Viewport>
            {options.map(([id, name]) => (
              <Select.Item key={id} value={id} className="choice-item">
                <Select.ItemText>{name}</Select.ItemText>
                <Select.ItemIndicator>
                  <Check size={14} />
                </Select.ItemIndicator>
              </Select.Item>
            ))}
          </Select.Viewport>
        </Select.Content>
      </Select.Portal>
    </Select.Root>
  );
}
export function Toggle({
  checked,
  onChange,
  label,
}: {
  checked: boolean;
  onChange: (value: boolean) => void;
  label: string;
}) {
  return (
    <Switch.Root
      checked={checked}
      onCheckedChange={onChange}
      aria-label={label}
      className="toggle"
    >
      <Switch.Thumb className="toggle-thumb" />
    </Switch.Root>
  );
}
