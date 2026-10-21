const normalize = (value) => String(value ?? '').normalize('NFKC').toLocaleLowerCase();

function workflowInputSummary(schema) {
  if (!schema || typeof schema !== 'object' || Array.isArray(schema)) return '';
  const properties = schema.properties && typeof schema.properties === 'object'
    ? Object.keys(schema.properties) : [];
  const required = new Set(Array.isArray(schema.required) ? schema.required : []);
  return properties.map((name) => `${name}${required.has(name) ? ' *' : ''}`).join(', ');
}

export function commandEntries({ skills = [], extensions = [] } = {}) {
  const entries = [];
  for (const skill of skills) {
    if (!skill?.enabled || !skill.available || !skill.name) continue;
    entries.push({
      kind: 'skill',
      name: String(skill.name),
      label: String(skill.name),
      source: skill.source === 'workspace' ? '工作区 Skill' : 'Skill',
      description: String(skill.description || ''),
      trigger: String(skill.trigger || 'auto'),
      icon: 'scroll-text',
    });
  }
  for (const extension of extensions) {
    if (!extension?.enabled || !extension.available || !extension.id) continue;
    for (const workflow of extension.workflows || []) {
      if (!workflow?.name) continue;
      entries.push({
        kind: 'workflow',
        name: String(workflow.name),
        label: String(workflow.name),
        source: String(extension.name || extension.id),
        extension: String(extension.id),
        description: String(workflow.description || ''),
        inputSchema: workflow.input_schema || null,
        inputSummary: workflowInputSummary(workflow.input_schema),
        icon: 'puzzle',
      });
    }
  }
  return entries.sort((a, b) => normalize(a.name).localeCompare(normalize(b.name)) ||
    (a.kind === b.kind ? normalize(a.source).localeCompare(normalize(b.source)) : a.kind === 'skill' ? -1 : 1));
}

export function commandQuery(prompt) {
  const value = String(prompt ?? '');
  if (!value.startsWith('/') || /\s/.test(value)) return null;
  return value.slice(1);
}

export function filterCommands(entries, query = '') {
  const needle = normalize(query).trim();
  if (!needle) return [...entries];
  return entries.filter((entry) => [entry.name, entry.source, entry.description]
    .some((value) => normalize(value).includes(needle)));
}

export function commandNeedsInput(entry) {
  if (!entry || entry.kind !== 'workflow') return false;
  const schema = entry.inputSchema;
  if (!schema || typeof schema !== 'object' || Array.isArray(schema)) return false;
  return Boolean((Array.isArray(schema.required) && schema.required.length > 0) ||
    (schema.properties && Object.keys(schema.properties).length > 0));
}

export function commandInputDefaults(entry) {
  const schema = entry?.inputSchema;
  if (!schema || typeof schema !== 'object' || Array.isArray(schema)) return '{}';
  const properties = schema.properties && typeof schema.properties === 'object'
    ? schema.properties : {};
  const value = {};
  for (const [name, definition] of Object.entries(properties)) {
    if (definition && Object.prototype.hasOwnProperty.call(definition, 'default')) {
      value[name] = definition.default;
    }
  }
  return JSON.stringify(value, null, 2);
}
