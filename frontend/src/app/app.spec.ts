import { provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { RouterTestingModule } from '@angular/router/testing';
import { vi } from 'vitest';
import { App } from './app';
import { AuthService } from './core/auth/auth.service';

describe('App', () => {
  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [App, RouterTestingModule],
      providers: [provideRouter([]), provideHttpClientTesting()],
    }).compileComponents();
  });

  it('should create the app', () => {
    const fixture = TestBed.createComponent(App);
    const app = fixture.componentInstance;
    expect(app).toBeTruthy();
  });

  it('should render the signed-in shell', async () => {
    const auth = TestBed.inject(AuthService);
    auth.currentUser.set({ sub: 'user-1', name: 'Test User', email: 'test@example.com' });
    vi.spyOn(auth, 'refreshUser').mockResolvedValue({ sub: 'user-1', name: 'Test User', email: 'test@example.com' });

    const fixture = TestBed.createComponent(App);
    await fixture.whenStable();
    const compiled = fixture.nativeElement as HTMLElement;

    expect(compiled.textContent).toContain('Git Sync');
    expect(compiled.textContent).toContain('Test User');
  });
});
