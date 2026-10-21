import { useTranslation } from "react-i18next";

export function CreationBaseDisplay({
  bases,
  errorCode,
}: {
  bases: string[];
  errorCode: string | null;
}) {
  const { t } = useTranslation();
  if (bases.length === 0) return null;

  return (
    <div className="border-t pt-2 text-muted-foreground" data-testid="changes-creation-base">
      <p className="font-medium text-foreground">{t("task:creationBaseLabel")}</p>
      <p
        className="max-w-[min(80vw,28rem)] break-words font-mono text-[11px]"
        title={bases.join(", ")}
      >
        {bases.join(", ")}
      </p>
      {errorCode && <p className="text-[11px]">{t("task:creationBaseUnavailable")}</p>}
    </div>
  );
}
