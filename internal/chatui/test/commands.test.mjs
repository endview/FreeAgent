import test from 'node:test';
import assert from 'node:assert/strict';
import { commandEntries, commandQuery, filterCommands, commandNeedsInput, commandInputDefaults } from '../src/commands.mjs';

const catalog = {
  skills: [
    { name: 'weekly-report', description: 'Write a report', source: 'user', trigger: 'manual', enabled: true, available: true },
    { name: 'disabled', description: 'Hidden', enabled: false, available: true },
  ],
  extensions: [
    {
      id: 'com.example.router', name: 'Router', enabled: true, available: true,
      workflows: [
        { name: 'triage', description: 'Choose a route', input_schema: {
          type: 'object', properties: { task: { type: 'string', default: 'hello' } }, required: ['task'],
        } },
        { name: 'weekly-report', description: 'Same command from an extension', input_schema: { type: 'object' } },
      ],
    },
    { id: 'com.example.off', enabled: false, available: true, workflows: [{ name: 'hidden' }] },
  ],
};

test('command catalog includes only callable enabled capabilities and preserves same-name sources', () => {
  const entries = commandEntries(catalog);
  assert.deepEqual(entries.map((entry) => `${entry.kind}:${entry.name}`), [
    'workflow:triage', 'skill:weekly-report', 'workflow:weekly-report',
  ]);
  assert.equal(entries[1].source, 'Skill');
  assert.equal(entries[2].source, 'Router');
});

test('slash query filters by name, source and description without treating normal text as a command', () => {
  const entries = commandEntries(catalog);
  assert.equal(commandQuery('/tri'), 'tri');
  assert.equal(commandQuery('plain'), null);
  assert.equal(commandQuery('/weekly report'), null);
  assert.deepEqual(filterCommands(entries, 'choose').map((entry) => entry.name), ['triage']);
  assert.deepEqual(filterCommands(entries, 'report').map((entry) => entry.kind), ['skill', 'workflow']);
});

test('workflow input helpers distinguish schema-driven input from an empty object', () => {
  const entries = commandEntries(catalog);
  const triage = entries.find((entry) => entry.name === 'triage');
  const report = entries.find((entry) => entry.kind === 'workflow' && entry.name === 'weekly-report');
  assert.equal(commandNeedsInput(triage), true);
  assert.equal(commandInputDefaults(triage), '{\n  "task": "hello"\n}');
  assert.equal(commandNeedsInput(report), false);
});
