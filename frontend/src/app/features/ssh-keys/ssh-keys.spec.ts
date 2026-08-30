import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { vi } from 'vitest';
import { SshKeysComponent } from './ssh-keys';
import { SSHKey } from '../../core/models';

describe('SshKeysComponent', () => {
  let fixture: ComponentFixture<SshKeysComponent>;
  let component: SshKeysComponent;
  let httpMock: HttpTestingController;

  const mockKeys: SSHKey[] = [
    {
      id: 1,
      name: 'deploy-key-1',
      key_type: 'ed25519',
      fingerprint: 'SHA256:abc123xyz',
      created_at: '2026-08-31T12:00:00Z',
    },
  ];

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [SshKeysComponent],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();

    fixture = TestBed.createComponent(SshKeysComponent);
    component = fixture.componentInstance;
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('should load and render SSH keys on initialization', async () => {
    fixture.detectChanges();

    const req = httpMock.expectOne('/api/ssh-keys');
    expect(req.request.method).toBe('GET');
    req.flush(mockKeys);

    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.keys().length).toBe(1);
    expect(component.keys()[0].name).toBe('deploy-key-1');
  });

  it('should validate key name and type in Reactive Forms', () => {
    fixture.detectChanges();
    const req = httpMock.expectOne('/api/ssh-keys');
    req.flush([]);

    expect(component.keyForm.valid).toBe(false);

    component.keyForm.patchValue({ name: 'k' });
    expect(component.keyForm.get('name')?.hasError('minlength')).toBe(true);

    component.keyForm.patchValue({ name: 'valid-key-name', key_type: 'ed25519' });
    expect(component.keyForm.valid).toBe(true);
  });

  it('should display backend error via AlertBannerComponent on generation failure', async () => {
    fixture.detectChanges();
    const initialReq = httpMock.expectOne('/api/ssh-keys');
    initialReq.flush([]);

    component.keyForm.setValue({ name: 'duplicate-key', key_type: 'ed25519' });
    component.submit();

    const postReq = httpMock.expectOne('/api/ssh-keys');
    expect(postReq.request.method).toBe('POST');
    postReq.flush(
      { error: { code: 'ssh_key_create_failed', message: 'SSH key with this name already exists' } },
      { status: 400, statusText: 'Bad Request' }
    );

    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.formError()).toBe('SSH key with this name already exists');
  });

  it('should delete key and remove it from local list on confirmation and success', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);

    fixture.detectChanges();
    const initialReq = httpMock.expectOne('/api/ssh-keys');
    initialReq.flush(mockKeys);

    await fixture.whenStable();
    expect(component.keys().length).toBe(1);

    component.deleteKey(mockKeys[0]);

    const delReq = httpMock.expectOne('/api/ssh-keys/1');
    expect(delReq.request.method).toBe('DELETE');
    delReq.flush({ deleted: true, id: 1 });

    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.keys().length).toBe(0);
    expect(component.pageSuccess()).toContain('deploy-key-1');
  });

  it('should not call API when confirmation is cancelled', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);

    fixture.detectChanges();
    const initialReq = httpMock.expectOne('/api/ssh-keys');
    initialReq.flush(mockKeys);

    await fixture.whenStable();
    expect(component.keys().length).toBe(1);

    component.deleteKey(mockKeys[0]);

    httpMock.expectNone('/api/ssh-keys/1');
    expect(component.keys().length).toBe(1);
  });

  it('should display error message on 409 Conflict when key is in use', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);

    fixture.detectChanges();
    const initialReq = httpMock.expectOne('/api/ssh-keys');
    initialReq.flush(mockKeys);

    await fixture.whenStable();

    component.deleteKey(mockKeys[0]);

    const delReq = httpMock.expectOne('/api/ssh-keys/1');
    expect(delReq.request.method).toBe('DELETE');
    delReq.flush(
      { error: { code: 'ssh_key_delete_failed', message: 'cannot delete key in use by one or more repositories' } },
      { status: 409, statusText: 'Conflict' }
    );

    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.keys().length).toBe(1);
    expect(component.pageError()).toBe('cannot delete key in use by one or more repositories');
  });
});

