/** A short "Browser on OS" for a User-Agent header, or the header itself when unknown. */
export function describeUserAgent(ua: string): { browser: string; os: string } {
  const browser =
    /Edg\//.test(ua) ? 'Edge'
      : /OPR\//.test(ua) ? 'Opera'
        : /Firefox\//.test(ua) ? 'Firefox'
          : /Chrome\//.test(ua) ? 'Chrome'
            : /Safari\//.test(ua) ? 'Safari'
              : /curl\//i.test(ua) ? 'curl'
                : ''
  const os =
    /iPhone|iPad/.test(ua) ? 'iOS'
      : /Android/.test(ua) ? 'Android'
        : /Mac OS X|Macintosh/.test(ua) ? 'macOS'
          : /Windows/.test(ua) ? 'Windows'
            : /Linux/.test(ua) ? 'Linux'
              : ''
  return { browser, os }
}
