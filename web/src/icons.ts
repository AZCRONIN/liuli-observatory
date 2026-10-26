// 线性图标，1.6 描边，currentColor
const w = (d: string) =>
    `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;

export const icons: Record<string, string> = {
    overview: w('<path d="M3 13h4l3-7 4 12 3-5h4"/>'),
    sites: w('<rect x="3" y="4" width="18" height="16" rx="2.5"/><path d="M3 9h18M7 6.5h.01M10 6.5h.01"/>'),
    ip: w('<circle cx="12" cy="12" r="8.5"/><path d="M3.5 12h17M12 3.5c2.6 2.4 3.8 5.2 3.8 8.5s-1.2 6.1-3.8 8.5c-2.6-2.4-3.8-5.2-3.8-8.5S9.4 5.9 12 3.5z"/>'),
    client: w('<rect x="3" y="4.5" width="13" height="10" rx="1.8"/><path d="M7 18.5h5M9.5 14.5v4"/><rect x="17.5" y="8.5" width="4" height="10" rx="1.2"/>'),
    spider: w('<circle cx="12" cy="13" r="3.5"/><path d="M12 9.5V7M8.5 13H4M19.5 13h-4M9.5 10.5 6 7M14.5 10.5 18 7M9.5 15.5 6 19M14.5 15.5 18 19"/>'),
    uri: w('<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>'),
    status: w('<rect x="3.5" y="12" width="3.5" height="8" rx="1"/><rect x="10.25" y="7" width="3.5" height="13" rx="1"/><rect x="17" y="3.5" width="3.5" height="16.5" rx="1"/>'),
    errors: w('<path d="M12 3.5 21 19.5H3z"/><path d="M12 10v4.5M12 17h.01"/>'),
    slow: w('<circle cx="12" cy="13" r="7.5"/><path d="M12 9v4l2.5 2M10 2.5h4"/>'),
    ban: w('<circle cx="12" cy="12" r="8.5"/><path d="M6 6l12 12"/>'),
    settings: w('<circle cx="12" cy="12" r="3"/><path d="M19 12a7 7 0 0 0-.1-1.2l2-1.6-2-3.4-2.4.9a7 7 0 0 0-2-1.2L14 3h-4l-.5 2.5a7 7 0 0 0-2 1.2l-2.4-.9-2 3.4 2 1.6A7 7 0 0 0 5 12c0 .4 0 .8.1 1.2l-2 1.6 2 3.4 2.4-.9a7 7 0 0 0 2 1.2L10 21h4l.5-2.5a7 7 0 0 0 2-1.2l2.4.9 2-3.4-2-1.6c.1-.4.1-.8.1-1.2z"/>'),
    menu: w('<path d="M4 7h16M4 12h16M4 17h16"/>'),
    sun: w('<circle cx="12" cy="12" r="4"/><path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4"/>'),
    moon: w('<path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>'),
    logout: w('<path d="M14 4h4a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-4M10 16l-4-4 4-4M6 12h10"/>'),
};
