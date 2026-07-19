import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ModelSelector } from './ModelSelector';

const inventory = {
  providers: [
    {
      provider: 'codex' as const,
      defaultModel: 'gpt-5.6-sol',
      status: 'ok' as const,
      models: [
        {
          id: 'gpt-5.6-sol',
          displayName: 'GPT-5.6 Sol',
          source: 'app_server',
          status: 'available' as const,
        },
      ],
    },
  ],
};

describe('ModelSelector', () => {
  it('shows provider default first and emits an available model', () => {
    const onChange = vi.fn();
    render(<ModelSelector provider="codex" value="" inventory={inventory} onChange={onChange} />);
    const select = screen.getByLabelText('Modelo');
    expect(select.querySelector('option')?.textContent).toContain('Padrão do provider');
    fireEvent.change(select, { target: { value: 'gpt-5.6-sol' } });
    expect(onChange).toHaveBeenCalledWith('gpt-5.6-sol');
  });

  it('accepts a custom model id', () => {
    const onChange = vi.fn();
    render(<ModelSelector provider="codex" value="" inventory={inventory} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText('Modelo'), { target: { value: '__custom__' } });
    fireEvent.change(screen.getByLabelText('Modelo personalizado'), {
      target: { value: 'vendor/custom' },
    });
    expect(onChange).toHaveBeenLastCalledWith('vendor/custom');
  });
});
