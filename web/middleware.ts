import { NextResponse, type NextRequest } from "next/server";

// Gate the dashboard on the presence of a session cookie.
//
// This is a redirect convenience, not an authorisation boundary: the cookie is
// a JWT that only the Go gateway can verify, so a forged cookie yields a shell
// whose every API call returns 401. The client-side check in AppShell catches
// that case and bounces back to the form.
//
// /api is deliberately excluded: the QR image, public order view, and status
// stream are unauthenticated by design, and the gateway enforces auth itself on
// the endpoints that need it.
const PUBLIC_PATHS = new Set(["/login"]);

export function middleware(request: NextRequest) {
  const { pathname } = request.nextUrl;
  const hasSession = request.cookies.has("paygate_session");

  if (PUBLIC_PATHS.has(pathname)) {
    if (hasSession) {
      return NextResponse.redirect(new URL("/", request.url));
    }
    return NextResponse.next();
  }

  if (!hasSession) {
    const url = new URL("/login", request.url);
    if (pathname !== "/") url.searchParams.set("next", pathname);
    return NextResponse.redirect(url);
  }

  return NextResponse.next();
}

export const config = {
  matcher: ["/((?!api|_next/static|_next/image|.*\\.(?:png|svg|ico)$).*)"],
};
