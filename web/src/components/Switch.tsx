/** Accessible on/off switch styled like a mobile toggle. */
export function Switch({ checked, onChange, disabled, label, hint }: {
  checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; label: string; hint?: string;
}) {
  return (
    <label className={`switch-row${disabled ? " disabled" : ""}`}>
      <span className="switch-text">
        <span>{label}</span>
        {hint && <span className="muted small">{hint}</span>}
      </span>
      <span className="switch">
        <input type="checkbox" role="switch" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
        <span className="switch-track"><span className="switch-thumb" /></span>
      </span>
    </label>
  );
}
