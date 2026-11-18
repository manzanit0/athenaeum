import {
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type TextareaHTMLAttributes,
  useEffect,
  useId,
  useRef,
} from "react";

/** The visual weight of a button. */
type Variant = "primary" | "secondary" | "danger" | "quiet";

const variants: Record<Variant, string> = {
  primary: "border-ink bg-ink text-sheet hover:bg-[#2c3531]",
  secondary: "border-ink bg-transparent text-ink hover:bg-ink/5",
  danger: "border-ribbon-deep bg-transparent text-ribbon-deep hover:bg-ribbon/10",
  quiet: "border-transparent bg-transparent text-ink underline underline-offset-4 hover:text-ribbon-deep px-0",
};

const buttonBase =
  "inline-flex items-center justify-center gap-2 rounded-sm border px-3.5 py-2 font-medium leading-tight " +
  "transition-colors disabled:cursor-not-allowed disabled:opacity-45";

/** Returns the class names of a button of the given variant. */
export function buttonClass(variant: Variant = "secondary"): string {
  return `${buttonBase} ${variants[variant]}`;
}

/** A button in one of the application's variants. */
export function Button({
  variant = "secondary",
  className = "",
  type = "button",
  ...rest
}: { variant?: Variant } & ButtonHTMLAttributes<HTMLButtonElement>) {
  return <button type={type} className={`${buttonClass(variant)} ${className}`} {...rest} />;
}

const fieldClass =
  "w-full rounded-sm border border-rule bg-sheet px-3 py-2 text-ink placeholder:text-soft/70 " +
  "focus:border-ink focus:outline-none focus-visible:outline-none focus:ring-2 focus:ring-ribbon/40";

/** A labelled single line text field. */
export function Field({
  label,
  hint,
  className = "",
  ...rest
}: { label: string; hint?: string } & InputHTMLAttributes<HTMLInputElement>) {
  const id = useId();
  return (
    <div className={className}>
      <label htmlFor={id} className="mb-1 block font-medium">
        {label}
      </label>
      <input id={id} className={fieldClass} aria-describedby={hint ? `${id}-hint` : undefined} {...rest} />
      {hint && (
        <p id={`${id}-hint`} className="mt-1 text-[0.87rem] text-soft">
          {hint}
        </p>
      )}
    </div>
  );
}

/** A labelled multi line text field. */
export function TextArea({
  label,
  className = "",
  ...rest
}: { label: string } & TextareaHTMLAttributes<HTMLTextAreaElement>) {
  const id = useId();
  return (
    <div className={className}>
      <label htmlFor={id} className="mb-1 block font-medium">
        {label}
      </label>
      <textarea id={id} className={`${fieldClass} min-h-32 leading-relaxed`} {...rest} />
    </div>
  );
}

/** The title block at the top of a page. */
export function PageHeader({ title, lead, children }: { title: string; lead?: ReactNode; children?: ReactNode }) {
  return (
    <header className="mb-8 flex flex-wrap items-end justify-between gap-x-6 gap-y-4 border-b border-rule pb-5">
      <div className="min-w-0">
        <h1 className="font-display text-[2.1rem] font-medium leading-tight tracking-[-0.01em]">{title}</h1>
        {lead && <p className="mt-1 max-w-[62ch] text-soft">{lead}</p>}
      </div>
      {children && <div className="flex flex-wrap items-center gap-3">{children}</div>}
    </header>
  );
}

/** A titled section of a page. */
export function Section({ title, children, aside }: { title: string; children: ReactNode; aside?: ReactNode }) {
  return (
    <section className="mb-10">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-3">
        <h2 className="font-display text-[1.35rem] font-medium">{title}</h2>
        {aside}
      </div>
      {children}
    </section>
  );
}

/** A message shown in place of content that is missing. */
export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="rounded-sm border border-dashed border-soft/50 px-6 py-10 text-center">
      <p className="font-display text-[1.25rem]">{title}</p>
      {children && <div className="mx-auto mt-2 max-w-[56ch] text-soft">{children}</div>}
    </div>
  );
}

/** A message explaining that something could not be loaded. */
export function Problem({ children }: { children: ReactNode }) {
  return (
    <p role="alert" className="rounded-sm border-l-4 border-ribbon bg-sheet px-4 py-3">
      {children}
    </p>
  );
}

/** A dialog asking the reader to confirm a destructive action. */
export function Confirm({
  open,
  title,
  children,
  action,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  children: ReactNode;
  action: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current;
    if (open && dialog && !dialog.open) {
      dialog.showModal();
    }
    if (!open && dialog?.open) {
      dialog.close();
    }
  }, [open]);
  return (
    <dialog
      ref={ref}
      onCancel={onCancel}
      onClose={onCancel}
      className="m-auto w-[min(28rem,calc(100vw-2rem))] rounded-sm border border-ink bg-sheet p-6 text-ink"
    >
      <h2 className="font-display text-[1.35rem] font-medium">{title}</h2>
      <div className="mt-2 text-soft">{children}</div>
      <div className="mt-6 flex justify-end gap-3">
        <Button onClick={onCancel}>Cancel</Button>
        <Button variant="danger" onClick={onConfirm}>
          {action}
        </Button>
      </div>
    </dialog>
  );
}
