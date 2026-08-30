import { HttpErrorResponse, HttpInterceptorFn } from '@angular/common/http';
import { catchError, throwError } from 'rxjs';
import { APP_LOGIN_ROUTE, AUTH_LOGIN_URL } from './../auth/auth.service';

export const authInterceptor: HttpInterceptorFn = (req, next) => {
  const cloned = req.clone({
    withCredentials: true,
  });

  return next(cloned).pipe(
    catchError((error: HttpErrorResponse) => {
      if (error.status === 401 && !req.url.startsWith('/auth/') && !req.url.startsWith(APP_LOGIN_ROUTE)) {
        window.location.href = AUTH_LOGIN_URL;
      }
      return throwError(() => error);
    }),
  );
};
