const groups = {
 Security: ['security', 'cybersecurity', 'audit', 'compliance', 'vulnerability', 'incident', 'access-control', 'identity', 'authentication', 'encryption', 'risk', 'confidential'],
 Business: ['business', 'finance', 'invoice', 'contract', 'legal', 'project', 'customer', 'meeting', 'report', 'presentation', 'planning', 'operations', 'sales', 'marketing'],
 General: ['general', 'personal', 'reference', 'notes', 'archive', 'image', 'document', 'school', 'research', 'receipt', 'travel', 'miscellaneous'],
};
export function TagSuggestions({ value, onChange, disabled = false }: { value: string; onChange: (value: string) => void; disabled?: boolean }) {
 const selected = new Set(value.split(',').map(tag => tag.trim().toLowerCase()).filter(Boolean));
 return <details><summary>Suggested tags</summary>{Object.entries(groups).map(([group, tags]) => <fieldset key={group} className="tag-suggestions"><legend>{group}</legend>{tags.map(tag => <button key={tag} type="button" aria-pressed={selected.has(tag)} disabled={disabled || (!selected.has(tag) && selected.size >= 20)} onClick={() => {
  const next = new Set(selected);
  if (next.has(tag)) next.delete(tag); else next.add(tag);
  onChange([...next].sort().join(', '));
 }}>{tag}</button>)}</fieldset>)}</details>;
}
