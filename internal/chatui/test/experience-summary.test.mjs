import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';

// Execute the component's actual pure parsing/publication functions. Keeping
// them in the owned TSX file avoids another production presentation module.
const source = readFileSync(new URL('../frontend/features/conversations/AnalysisDraft.tsx', import.meta.url), 'utf8');
const boundary = source.indexOf('export default function AnalysisDraft(');
assert.ok(boundary > 0);
const code = source.slice(0, boundary).replace(/^import .*;\r?$/gm, '');
const compiled = stripTypeScriptTypes(code);
const {candidate, analysisPublication, analysisMemoryRequestKey} = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`);

for (const name of ['deployment', 'convention', 'unknown']) {
  test(`${name} synthetic evidence survives parsing, editing and both save formats`, () => {
    const sample = JSON.parse(readFileSync(new URL(`../../../optional-skills/experience-summary/examples/${name}.json`, import.meta.url), 'utf8'));
    const parsed = candidate(JSON.stringify(sample.candidate));
    assert.deepEqual(parsed.sources, sample.candidate.sources);
    assert.deepEqual(parsed.limitations, sample.candidate.limitations);
    assert.deepEqual(parsed.source_caveats, sample.candidate.source_caveats);
    assert.equal(parsed.verification, sample.candidate.verification);
    for (const kind of ['memory', 'skill']) {
      const edited = {...parsed, kind, body:'Edited body.', document:'---\nname: fixture\ntrigger: manual\n---\nEdited steps.'};
      const saved = analysisPublication(edited);
      for (const evidence of [...parsed.sources, ...parsed.source_caveats, ...parsed.limitations, parsed.verification]) assert.ok(saved.includes(evidence), `lost evidence in ${kind}: ${evidence}`);
      assert.ok(saved.startsWith(kind === 'skill' ? '---\n' : 'Edited body.'));
      assert.equal(analysisPublication(edited), saved, 'preview and save must be deterministic');
    }
    if (name === 'unknown') {
      assert.equal(parsed.kind, 'draft');
      assert.match(analysisPublication(parsed), /UNKNOWN/);
      assert.match(analysisPublication(parsed), /禁止盲目重试/);
    }
  });
}

test('fenced JSON, structured source caveats and verification arrays retain their content', () => {
  const value = {kind:'memory', body:'A convention.', sources:[{run_id:'synthetic', caveat:'delivery UNKNOWN'}], source_caveats:'Only public evidence.', limitations:['Not verified.'], verification:['Inspect the receipt.', 'Ask the user.']};
  const parsed = candidate('```json\n' + JSON.stringify(value) + '\n```');
  const saved = analysisPublication(parsed);
  for (const text of ['synthetic','delivery UNKNOWN','Only public evidence.','Not verified.','Inspect the receipt.','Ask the user.']) assert.ok(saved.includes(text));
});

test('non-JSON answers stay editable drafts and empty evidence adds no sections', () => {
  for (const text of ['Unresolved UNKNOWN; verify first.', '["still a draft"]']) {
    const parsed = candidate(text);
    assert.equal(parsed.kind, 'draft');
    assert.equal(analysisPublication(parsed), text);
  }
  const parsed = candidate('{"kind":"memory","body":"Short convention."}');
  assert.equal(analysisPublication({...parsed, sources:['',' '], limitations:['']}), 'Short convention.');
});

test('identical reviewed Memory saves keep the same request key across remounts', async () => {
  const first = await analysisMemoryRequestKey('run-one', 'Title', 'Body with UNKNOWN caveats.');
  assert.match(first, /^analysis-memory\/[a-f0-9]{64}$/);
  assert.equal(await analysisMemoryRequestKey('run-one', 'Title', 'Body with UNKNOWN caveats.'), first);
  assert.notEqual(await analysisMemoryRequestKey('run-one', 'Title', 'Edited body.'), first);
  assert.notEqual(await analysisMemoryRequestKey('run-two', 'Title', 'Body with UNKNOWN caveats.'), first);
});
