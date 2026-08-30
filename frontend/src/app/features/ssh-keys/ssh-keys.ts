import { HttpClient } from '@angular/common/http';
import { Component, OnInit, inject, signal } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { firstValueFrom } from 'rxjs';
import { SSHKey } from '../../core/models';
import { getApiErrorMessage } from '../../core/http/api-error';
import { AlertBannerComponent } from '../../shared/alert-banner/alert-banner.component';

@Component({
  selector: 'app-ssh-keys',
  standalone: true,
  imports: [ReactiveFormsModule, AlertBannerComponent],
  template: `
    <div class="space-y-6">
      <div class="flex items-center justify-between">
        <div>
          <h2 class="text-2xl font-semibold text-white">SSH Keys</h2>
          <p class="text-sm text-slate-400">Deploy keys used by pull and push remotes.</p>
        </div>
      </div>

      <!-- Page-level alerts -->
      @if (pageError()) {
        <app-alert-banner [message]="pageError()" type="error" [dismissible]="true" (dismiss)="pageError.set('')" />
      }
      @if (pageSuccess()) {
        <app-alert-banner [message]="pageSuccess()" type="success" [dismissible]="true" (dismiss)="pageSuccess.set('')" />
      }

      <div class="rounded-2xl border border-slate-800 bg-slate-900 p-5">
        <div class="mb-4 text-lg font-semibold text-white">Generate SSH key</div>

        @if (formError()) {
          <div class="mb-4">
            <app-alert-banner [message]="formError()" type="error" [dismissible]="true" (dismiss)="formError.set('')" />
          </div>
        }

        <form [formGroup]="keyForm" (ngSubmit)="submit()">
          <div class="flex flex-col gap-4 md:flex-row md:items-start">
            <div class="flex-1">
              <label class="block text-sm text-slate-300">
                <span class="mb-1 block font-medium">Key name <span class="text-rose-400">*</span></span>
                <input
                  type="text"
                  formControlName="name"
                  class="w-full rounded-xl border bg-slate-950 px-3 py-2 text-white transition-colors focus:outline-none"
                  [class.border-slate-700]="!isFieldInvalid('name')"
                  [class.border-rose-500]="isFieldInvalid('name')"
                  [class.focus:border-sky-400]="!isFieldInvalid('name')"
                  placeholder="e.g. prod-deploy-key"
                />
              </label>
              @if (isFieldInvalid('name')) {
                <div class="mt-1 text-xs text-rose-400">
                  @if (keyForm.get('name')?.hasError('required')) {
                    Key name is required.
                  } @else if (keyForm.get('name')?.hasError('minlength')) {
                    Key name must be at least 2 characters.
                  }
                </div>
              }
            </div>

            <div class="w-full md:w-48">
              <label class="block text-sm text-slate-300">
                <span class="mb-1 block font-medium">Type</span>
                <select
                  formControlName="key_type"
                  class="w-full rounded-xl border border-slate-700 bg-slate-950 px-3 py-2 text-white focus:border-sky-400 focus:outline-none"
                >
                  <option value="ed25519">ed25519 (recommended)</option>
                  <option value="rsa">rsa</option>
                </select>
              </label>
            </div>

            <div class="pt-6">
              <button
                type="submit"
                [disabled]="saving() || keyForm.invalid"
                class="flex items-center gap-2 rounded-xl bg-sky-500 px-5 py-2 font-medium text-slate-950 transition hover:bg-sky-400 disabled:opacity-60 disabled:cursor-not-allowed"
              >
                @if (saving()) {
                  <i class="fa-solid fa-circle-notch fa-spin text-sm"></i>
                  <span>Generating…</span>
                } @else {
                  <i class="fa-solid fa-plus text-sm"></i>
                  <span>Generate key</span>
                }
              </button>
            </div>
          </div>
        </form>
      </div>

      @if (loading()) {
        <div class="rounded-2xl border border-slate-800 bg-slate-900 p-6 text-slate-300">Loading SSH keys…</div>
      } @else {
        <div class="grid gap-4 md:grid-cols-2">
          @for (key of keys(); track key.id) {
            <div class="flex flex-col justify-between rounded-2xl border border-slate-800 bg-slate-900 p-5 transition-all hover:border-slate-700">
              <div>
                <div class="mb-3 flex items-center justify-between">
                  <div class="text-base font-semibold text-white">{{ key.name }}</div>
                  <span class="rounded-full bg-sky-500/15 px-2.5 py-0.5 text-xs text-sky-300 font-mono uppercase tracking-wider">
                    {{ key.key_type }}
                  </span>
                </div>
                <p class="text-xs font-mono text-slate-400 break-all bg-slate-950/60 p-2 rounded-lg border border-slate-800">
                  <span class="text-slate-500">SHA256:</span> {{ key.fingerprint }}
                </p>
              </div>

              <div class="mt-5 flex items-center justify-between border-t border-slate-800 pt-3">
                <button
                  type="button"
                  (click)="viewPublicKey(key.id)"
                  class="inline-flex items-center gap-1.5 text-sm font-medium text-sky-400 hover:text-sky-300 transition-colors"
                >
                  <i class="fa-regular fa-eye text-xs"></i>
                  <span>View public key</span>
                </button>

                <button
                  type="button"
                  (click)="deleteKey(key)"
                  [disabled]="deletingId() === key.id"
                  class="inline-flex items-center gap-1.5 text-xs text-rose-400 hover:text-rose-300 rounded-lg px-2.5 py-1 transition-colors hover:bg-rose-500/10 disabled:opacity-50"
                  title="Remove SSH key"
                >
                  @if (deletingId() === key.id) {
                    <i class="fa-solid fa-circle-notch fa-spin text-xs"></i>
                    <span>Removing…</span>
                  } @else {
                    <i class="fa-solid fa-trash-can text-xs"></i>
                    <span>Remove</span>
                  }
                </button>
              </div>
            </div>
          } @empty {
            <div class="rounded-2xl border border-dashed border-slate-700 bg-slate-900 p-8 text-center text-slate-400 md:col-span-2">
              <i class="fa-solid fa-key mb-2 text-2xl text-slate-600 block"></i>
              <p class="font-medium text-slate-300">No SSH keys found</p>
              <p class="text-sm text-slate-500 mt-1">Generate an SSH key above to configure pull and push authentication.</p>
            </div>
          }
        </div>
      }

      @if (publicKey()) {
        <div class="relative rounded-2xl border border-slate-800 bg-slate-950 p-5 text-sm text-slate-200">
          <div class="flex items-center justify-between mb-2">
            <div class="text-xs uppercase tracking-[0.18em] text-slate-500 font-semibold">Public Key ({{ activeKeyName() }})</div>
            <div class="flex items-center gap-2">
              <button
                type="button"
                (click)="copyPublicKey()"
                class="inline-flex items-center gap-1.5 text-xs font-medium px-2.5 py-1 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-200 transition"
              >
                <i [class]="copied() ? 'fa-solid fa-check text-emerald-400' : 'fa-regular fa-copy'"></i>
                <span>{{ copied() ? 'Copied!' : 'Copy to clipboard' }}</span>
              </button>
              <button
                type="button"
                (click)="publicKey.set(''); activeKeyName.set('')"
                class="text-slate-500 hover:text-slate-300 p-1"
                aria-label="Close"
              >
                <i class="fa-solid fa-xmark"></i>
              </button>
            </div>
          </div>
          <pre class="overflow-x-auto whitespace-pre-wrap break-all rounded-xl bg-slate-900/90 p-3 text-xs font-mono text-emerald-300 border border-slate-800 select-all">{{ publicKey() }}</pre>
        </div>
      }
    </div>
  `,
})
export class SshKeysComponent implements OnInit {
  private readonly http = inject(HttpClient);

  readonly keys = signal<SSHKey[]>([]);
  readonly loading = signal(true);
  readonly saving = signal(false);
  readonly deletingId = signal<number | null>(null);
  readonly formError = signal('');
  readonly pageError = signal('');
  readonly pageSuccess = signal('');
  readonly publicKey = signal('');
  readonly activeKeyName = signal('');
  readonly copied = signal(false);

  readonly keyForm = new FormGroup({
    name: new FormControl('', {
      nonNullable: true,
      validators: [Validators.required, Validators.minLength(2), Validators.maxLength(100)],
    }),
    key_type: new FormControl<'ed25519' | 'rsa'>('ed25519', {
      nonNullable: true,
      validators: [Validators.required],
    }),
  });

  async ngOnInit(): Promise<void> {
    await this.loadKeys();
  }

  isFieldInvalid(fieldName: 'name' | 'key_type'): boolean {
    const control = this.keyForm.get(fieldName);
    return !!(control && control.invalid && (control.dirty || control.touched));
  }

  async submit(): Promise<void> {
    if (this.keyForm.invalid) {
      this.keyForm.markAllAsTouched();
      return;
    }

    this.saving.set(true);
    this.formError.set('');
    this.pageError.set('');
    this.pageSuccess.set('');

    const formVal = this.keyForm.getRawValue();

    try {
      await firstValueFrom(this.http.post<SSHKey>('/api/ssh-keys', {
        name: formVal.name.trim(),
        key_type: formVal.key_type,
      }));
      this.pageSuccess.set(`SSH key "${formVal.name.trim()}" generated successfully.`);
      this.keyForm.reset({
        name: '',
        key_type: 'ed25519',
      });
      await this.loadKeys();
    } catch (err) {
      this.formError.set(getApiErrorMessage(err, 'Could not generate the SSH key. Check the name and type.'));
    } finally {
      this.saving.set(false);
    }
  }

  async viewPublicKey(id: number): Promise<void> {
    const target = this.keys().find((k) => k.id === id);
    this.activeKeyName.set(target ? target.name : `Key #${id}`);
    this.copied.set(false);
    try {
      const response = await firstValueFrom(this.http.get<{ public_key: string }>(`/api/ssh-keys/${id}/public-key`));
      this.publicKey.set(response.public_key);
    } catch (err) {
      this.publicKey.set('');
      this.pageError.set(getApiErrorMessage(err, 'Unable to load the public key.'));
    }
  }

  async copyPublicKey(): Promise<void> {
    if (!this.publicKey()) return;
    try {
      await navigator.clipboard.writeText(this.publicKey());
      this.copied.set(true);
      setTimeout(() => this.copied.set(false), 2000);
    } catch {
      // Fallback
    }
  }

  async deleteKey(key: SSHKey): Promise<void> {
    if (!confirm(`Are you sure you want to remove SSH key "${key.name}"? This action cannot be undone.`)) {
      return;
    }

    this.deletingId.set(key.id);
    this.pageError.set('');
    this.pageSuccess.set('');

    try {
      await firstValueFrom(this.http.delete(`/api/ssh-keys/${key.id}`));
      this.pageSuccess.set(`SSH key "${key.name}" removed successfully.`);
      if (this.activeKeyName() === key.name) {
        this.publicKey.set('');
        this.activeKeyName.set('');
      }
      this.keys.update((list) => list.filter((k) => k.id !== key.id));
    } catch (err) {
      this.pageError.set(getApiErrorMessage(err, `Unable to remove SSH key "${key.name}".`));
    } finally {
      this.deletingId.set(null);
    }
  }

  private async loadKeys(): Promise<void> {
    try {
      const keys = await firstValueFrom(this.http.get<SSHKey[]>('/api/ssh-keys'));
      this.keys.set(keys ?? []);
      this.pageError.set('');
    } catch (err) {
      this.keys.set([]);
      this.pageError.set(getApiErrorMessage(err, 'Unable to load SSH keys.'));
    } finally {
      this.loading.set(false);
    }
  }
}
