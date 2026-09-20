import * as DialogPrimitive from '@radix-ui/react-dialog';
import { Slot } from '@radix-ui/react-slot';
import { X } from 'lucide-react';
import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) { return twMerge(clsx(inputs)); }
export function Button({ className, variant = 'primary', asChild, ...props }:
 ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'secondary' | 'ghost' | 'danger'; asChild?: boolean }) {
 const Component = asChild ? Slot : 'button';
 return <Component className={cn('button', className)} data-variant={variant} {...props} />;
}
export function Dialog({ open, onClose, title, description, children, wide = false }:
 { open: boolean; onClose: () => void; title: string; description?: string; children: ReactNode; wide?: boolean }) {
 return <DialogPrimitive.Root open={open} onOpenChange={value => { if (!value) onClose(); }}>
  <DialogPrimitive.Portal>
   <DialogPrimitive.Overlay className="dialog-overlay" />
   <DialogPrimitive.Content className={cn('dialog', wide && 'dialog-wide')} {...(description ? {} : { 'aria-describedby': undefined })}
    onCloseAutoFocus={event => {
     // Action buttons live outside a Radix Trigger; restore the last explicit focus target.
     const target = document.querySelector<HTMLElement>('[data-restore-focus="true"]');
     if (target) { event.preventDefault(); target.focus(); target.removeAttribute('data-restore-focus'); }
    }}>
    <div className="dialog-heading"><DialogPrimitive.Title>{title}</DialogPrimitive.Title>
     <DialogPrimitive.Close asChild><Button variant="ghost" aria-label="Close dialog"><X size={18} /></Button></DialogPrimitive.Close>
    </div>
    {description && <DialogPrimitive.Description className="muted">{description}</DialogPrimitive.Description>}
    {children}
   </DialogPrimitive.Content>
  </DialogPrimitive.Portal>
 </DialogPrimitive.Root>;
}
export function rememberFocus(event: React.MouseEvent<HTMLElement>) {
 document.querySelectorAll('[data-restore-focus]').forEach(node => node.removeAttribute('data-restore-focus'));
 event.currentTarget.dataset.restoreFocus = 'true';
}
export function Notice({ children, error = false }: { children: ReactNode; error?: boolean }) {
 return <div className={cn('notice', error && 'notice-error')} role={error ? 'alert' : 'status'}>{children}</div>;
}
export function EmptyState({ title, children }: { title: string; children: ReactNode }) {
 return <div className="empty-state"><h3>{title}</h3><p className="muted">{children}</p></div>;
}
