// SPDX-License-Identifier: AGPL-3.0-or-later
// Typed confirmation of a name before a destructive action (pool, share, user…).
import { useTranslation } from 'react-i18next'
import { Field } from './Field'

/** "Type <name> to confirm", the name shown as typed (labels are set in capitals). */
export function ConfirmName({
  name,
  value,
  onChange,
  word = false,
}: {
  name: string
  value: string
  onChange: (value: string) => void
  /** A word to type ("restore", "docker"), not a name: "Confirmed." once typed. */
  word?: boolean
}) {
  const { t } = useTranslation()
  const MARK = '\u0001'
  const [before = '', ...after] = t('repair.typeName', { name: MARK }).split(MARK)
  return (
    <Field
      label={
        <>
          {before}
          <span className="font-semibold tracking-normal text-foreground normal-case">{name}</span>
          {after.join(name)}
        </>
      }
      name={`confirm-${name}`}
      mono
      autoComplete="off"
      autoCapitalize="none"
      spellCheck={false}
      className="max-w-md"
      value={value}
      onChange={(event) => onChange(event.target.value)}
      success={value === name ? (word ? t('wipe.confirmed') : t('repair.nameConfirmed')) : null}
    />
  )
}
