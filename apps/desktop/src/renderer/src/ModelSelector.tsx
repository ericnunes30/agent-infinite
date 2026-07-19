import { useEffect, useId, useState } from 'react';
import type { ModelInventory, Provider } from './domain';

interface Props {
  readonly provider: Provider;
  readonly value: string;
  readonly inventory: ModelInventory;
  readonly onChange: (value: string) => void;
  readonly label?: string;
}

export function ModelSelector({
  provider,
  value,
  inventory,
  onChange,
  label = 'Modelo',
}: Props): React.JSX.Element {
  const listId = useId();
  const catalog = inventory.providers.find((item) => item.provider === provider);
  const defaultLabel = catalog?.defaultModel ?? 'automático';
  const models = catalog?.models ?? [];
  const isCustomValue = value !== '' && !models.some((model) => model.id === value);
  const [customMode, setCustomMode] = useState(isCustomValue);
  useEffect(() => setCustomMode(false), [provider]);
  useEffect(() => {
    if (value !== '' && !isCustomValue) setCustomMode(false);
  }, [isCustomValue, value]);
  const customActive = customMode || isCustomValue;

  return (
    <div className="model-selector">
      <label htmlFor={listId}>{label}</label>
      <select
        id={listId}
        value={customActive ? '__custom__' : value}
        onChange={(event) => {
          if (event.target.value === '__custom__') {
            setCustomMode(true);
            if (!isCustomValue) onChange('');
            return;
          }
          setCustomMode(false);
          onChange(event.target.value);
        }}
        aria-describedby={`${listId}-help`}
      >
        <option value="">Padrão do provider — {defaultLabel}</option>
        {models.map((model) => (
          <option key={model.id} value={model.id}>
            {model.displayName && model.displayName !== model.id
              ? `${model.displayName} — ${model.id}`
              : model.id}{' '}
            · {model.status}
          </option>
        ))}
        <option value="__custom__">Usar ID personalizado</option>
      </select>
      {customActive ? (
        <input
          aria-label={`${label} personalizado`}
          value={value}
          required
          maxLength={240}
          placeholder={provider === 'pi' || provider === 'opencode' ? 'provider/model' : 'model-id'}
          onChange={(event) => onChange(event.target.value.trim())}
          autoFocus
        />
      ) : null}
      <small id={`${listId}-help`}>
        Deixe vazio para usar o padrão. Você também pode informar um ID personalizado.
      </small>
    </div>
  );
}
