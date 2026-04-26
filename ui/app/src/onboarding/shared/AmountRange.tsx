// AmountRange — currency-tagged numeric range input shared between
// the job (salary), funding, and any future-money kind flows. We keep
// min/max as numbers (0 = unset) and the currency as a free-form
// 3-letter ISO 4217 string for now; the matcher normalises.

import type { ChangeEvent, JSX } from "react";

export function AmountRange({
  label,
  currency,
  amountMin,
  amountMax,
  onAmountMin,
  onAmountMax,
  onCurrency,
}: {
  label: string;
  currency: string;
  amountMin: number;
  amountMax: number;
  onAmountMin: (v: number) => void;
  onAmountMax: (v: number) => void;
  onCurrency: (v: string) => void;
}): JSX.Element {
  return (
    <div className="space-y-2">
      <label className="block text-sm font-medium text-gray-700">{label}</label>
      <div className="flex gap-2">
        <input
          type="number"
          placeholder="min"
          value={amountMin || ""}
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            onAmountMin(Number(e.currentTarget.value))
          }
          className="input-field flex-1"
        />
        <input
          type="number"
          placeholder="max"
          value={amountMax || ""}
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            onAmountMax(Number(e.currentTarget.value))
          }
          className="input-field flex-1"
        />
        <input
          type="text"
          placeholder="USD"
          value={currency}
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            onCurrency(e.currentTarget.value.toUpperCase())
          }
          className="input-field w-24"
          maxLength={3}
        />
      </div>
    </div>
  );
}
