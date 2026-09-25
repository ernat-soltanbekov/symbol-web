"use strict";

const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

// Run the shipped scripts against a deliberately small DOM. In particular,
// innerHTML is forbidden: API strings must remain text, including HTML-like input.
class Element {
    constructor(document, tagName = "div") {
        this.document = document;
        this.tagName = tagName;
        this.children = [];
        this.listeners = new Map();
        this.attributes = new Map();
        this.hidden = false;
        this.disabled = false;
        this.value = "";
        this.className = "";
        this.open = false;
        this._text = "";
        this.classList = {
            add: (name) => { this.className = [...new Set([...this.className.split(" ").filter(Boolean), name])].join(" "); },
            remove: (name) => { this.className = this.className.split(" ").filter((item) => item !== name).join(" "); },
            contains: (name) => this.className.split(" ").includes(name),
            toggle: (name, force) => {
                const enabled = force ?? !this.classList.contains(name);
                this.classList[enabled ? "add" : "remove"](name);
                return enabled;
            },
        };
    }
    set innerHTML(_) { throw new Error("Untrusted API text must not be inserted as HTML"); }
    set textContent(value) { this._text = String(value); this.children = []; }
    get textContent() { return this._text + this.children.map((child) => child.textContent).join(""); }
    get childElementCount() { return this.children.length; }
    get firstElementChild() { return this.children[0] || null; }
    addEventListener(type, listener) {
        if (!this.listeners.has(type)) this.listeners.set(type, []);
        this.listeners.get(type).push(listener);
    }
    emit(type, properties = {}) {
        const event = { type, target: this, preventDefault() {}, ...properties };
        return Promise.all((this.listeners.get(type) || []).map((listener) => listener(event)));
    }
    dispatchEvent(event) { return this.emit(event.type); }
    replaceChildren(...children) { this._text = ""; this.children = [...children]; }
    append(...children) { children.forEach((child) => { child.parent = this; }); this.children.push(...children); }
    appendChild(child) { this.append(child); return child; }
    contains(element) { return this === element || this.children.some((child) => child.contains(element)); }
    querySelectorAll(tagName) {
        return this.children.flatMap((child) => [
            ...(child.tagName === tagName ? [child] : []), ...child.querySelectorAll(tagName),
        ]);
    }
    querySelector(tagName) { return this.querySelectorAll(tagName)[0] || null; }
    setAttribute(name, value) { this.attributes.set(name, String(value)); }
    getAttribute(name) { return this.attributes.get(name) ?? null; }
    removeAttribute(name) { this.attributes.delete(name); }
    remove() {
        if (this.parent) this.parent.children = this.parent.children.filter((child) => child !== this);
    }
    click() { return this.emit("click"); }
    focus() {
        this.document.activeElement = this;
        this.document.emit("focusin", { target: this });
    }
    showModal() { this.open = true; }
    close() {
        if (!this.open) return;
        this.open = false;
        this.emit("close");
    }
}

function clock() {
    let now = 0;
    let nextID = 0;
    const pending = new Map();
    return {
        setTimeout(callback, delay) {
            const id = ++nextID;
            pending.set(id, { callback, due: now + delay });
            return id;
        },
        clearTimeout(id) { pending.delete(id); },
        advance(milliseconds) {
            const target = now + milliseconds;
            for (;;) {
                const next = [...pending.entries()].sort((a, b) => a[1].due - b[1].due)[0];
                if (!next || next[1].due > target) break;
                now = next[1].due;
                pending.delete(next[0]);
                next[1].callback();
            }
            now = target;
        },
    };
}

function setup(script) {
    const document = new Element(null, "document");
    document.document = document;
    const elements = new Map();
    document.getElementById = (id) => {
        if (!elements.has(id)) elements.set(id, new Element(document));
        return elements.get(id);
    };
    const banners = ["standard", "shadow", "thinkertoy"];
    const labels = new Map(banners.map((banner) => [banner, new Element(document, "label")]));
    document.querySelectorAll = () => [...labels.values()].filter((label) => label.classList.contains("ai-pick"));
    document.querySelector = (selector) => labels.get(selector.match(/data-banner="([^"]+)"/)[1]);
    const input = document.getElementById("text-input");
    const form = document.getElementById("symbol-form");
    const requests = [];
    const selections = [];
    const fills = [];
    const notices = [];
    const timers = clock();
    const studio = {
        input,
        form,
        banners,
        validate: (text) => !text.trim() || text.length > 1000 || /[^\x20-\x7E\n\r]/.test(text) ? "Invalid input" : "",
        element(tagName, className, text) {
            const element = new Element(document, tagName);
            element.className = className || "";
            if (text !== undefined) element.textContent = text;
            return element;
        },
        request(url, body, signal) {
            // Deliberately allow completion after abort. Version guards must protect
            // the UI even if the network or JSON parsing wins the cancellation race.
            return new Promise((resolve, reject) => requests.push({ url, body, signal, resolve, reject }));
        },
        setText(text, banner) {
            fills.push({ text, banner });
            input.value = text;
            if (banner) studio.selectBanner(banner);
            input.emit("input");
            input.focus();
        },
        selectBanner(banner) {
            selections.push(banner);
            form.emit("change", { target: { name: "banner", value: banner } });
        },
        setBusy(button, busy) { button.disabled = busy; button.setAttribute("aria-busy", busy); },
        notify(message, isError) { notices.push({ message, isError }); },
    };
    document.getElementById("suggestions-dropdown").hidden = true;
    document.getElementById("recommendation-result").hidden = true;
    document.getElementById("ai-recommend-btn").textContent = "Recommend";
    const window = { SymbolStudio: studio, setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout };
    const filename = path.join(__dirname, "..", "static", "js", script);
    vm.runInNewContext(readFileSync(filename, "utf8"), { window, document, AbortController }, { filename });
    return {
        document, input, form, requests, timers, selections, fills, labels, notices,
        get: document.getElementById,
        type(text) { input.value = text; input.emit("input"); },
    };
}

async function flushResponses() {
    await Promise.resolve();
    await Promise.resolve();
}

function visibleSuggestions(harness) {
    return harness.get("suggestions-dropdown").children.map((child) => child.textContent);
}

// Exercise app.js itself, including its transport and UI state, instead of using
// the request stub supplied to the smaller feature tests above.
function setupApp(options = {}) {
    const document = new Element(null, "document");
    document.document = document;
    document.body = new Element(document, "body");
    const elements = new Map();
    const links = [];
    const get = (id) => {
        if (!elements.has(id)) elements.set(id, new Element(document));
        return elements.get(id);
    };
    document.getElementById = get;
    document.createElement = (tag) => {
        const node = new Element(document, tag);
        if (tag === "a") {
            links.push(node);
            node.clickCount = 0;
            node.click = () => {
                if (options.linkClickError) throw new Error("Download blocked");
                node.clickCount += 1;
            };
        }
        return node;
    };
    document.querySelectorAll = () => [];
    document.createRange = () => ({ selectNodeContents: (node) => { selected.push(node); } });
    const input = get("text-input");
    const form = get("symbol-form");
    input.value = options.text || "";
    const radios = ["standard", "shadow", "thinkertoy"].map((banner) => {
        const radio = get("banner-" + banner);
        radio.name = "banner";
        radio.value = banner;
        radio.checked = banner === (options.banner || "standard");
        radio.addEventListener("change", () => {
            radios.forEach((other) => { other.checked = other === radio; });
            return form.emit("change", { target: radio });
        });
        return radio;
    });
    form.querySelector = () => radios.find((radio) => radio.checked);
    get("generate-btn").append(new Element(document, "span"));
    get("ascii-result").hidden = options.result === undefined;
    if (options.result !== undefined) get("ascii-result").textContent = options.result;
    const timers = clock();
    const requests = [];
    const storage = new Map(options.history === undefined ? [] : [["symbol-web.history.v1", options.history]]);
    const localStorage = {
        getItem(key) { if (options.storageReadError) throw new Error("Storage denied"); return storage.get(key) ?? null; },
        setItem(key, value) { if (options.storageWriteError) throw new Error("Storage full"); storage.set(key, value); },
        removeItem(key) { if (options.storageWriteError) throw new Error("Storage denied"); storage.delete(key); },
    };
    const selected = [];
    const selection = { removeAllRanges() {}, addRange() {} };
    const downloads = [];
    const revoked = [];
    const URL = {
        createObjectURL(blob) {
            if (options.objectURLError) throw new Error("Object URLs unavailable");
            downloads.push(blob);
            return "blob:test/" + downloads.length;
        },
        revokeObjectURL(url) { revoked.push(url); },
    };
    function fetch(url, init) {
        return new Promise((resolve, reject) => {
            const request = { url, ...init, resolve, reject };
            requests.push(request);
            if (options.abortFetch) init.signal.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")), { once: true });
        });
    }
    const navigator = { clipboard: options.clipboard };
    const window = {
        fetch, AbortController, setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout,
        matchMedia: () => ({ matches: false }),
        getSelection: () => options.selectionUnavailable ? null : selection,
    };
    const filename = path.join(__dirname, "..", "static", "js", "app.js");
    vm.runInNewContext(readFileSync(filename, "utf8"), {
        document, window, fetch, navigator, localStorage, AbortController, DOMException, Event, URLSearchParams, Blob, URL, TypeError,
    }, { filename });
    return {
        document, window, input, form, get, requests, timers, storage, selected, downloads, revoked, links,
        type(text) { input.value = text; return input.emit("input"); },
        respond(data, { index = requests.length - 1, ok = true, jsonError } = {}) {
            requests[index].resolve({ ok, json: () => jsonError ? Promise.reject(jsonError) : Promise.resolve(data) });
        },
    };
}

test("suggestions wait 300 ms after the last edit", () => {
    const h = setup("suggestions.js");
    h.type("Hel");
    h.timers.advance(299);
    assert.equal(h.requests.length, 0);
    h.type("Hello");
    h.timers.advance(299);
    assert.equal(h.requests.length, 0);
    h.timers.advance(1);
    assert.equal(h.requests.length, 1);
    assert.equal(h.requests[0].url, "/api/suggest");
    assert.equal(h.requests[0].body.text, "Hello");
    assert.equal(h.requests[0].signal.aborted, false);
});

test("short, blank and unsupported input never requests suggestions", () => {
    const h = setup("suggestions.js");
    for (const value of ["", "a", "ab", "  ab  ", "   ", "Привет"]) {
        h.type(value);
        h.timers.advance(500);
    }
    assert.equal(h.requests.length, 0);
    assert.equal(h.get("suggestions-dropdown").hidden, true);
});

test("editing aborts immediately and a stale response cannot reopen the dropdown", async () => {
    const h = setup("suggestions.js");
    h.type("Hello");
    h.timers.advance(300);
    assert.equal(h.get("suggestions-dropdown").hidden, false);
    h.type("Welcome");
    assert.equal(h.requests[0].signal.aborted, true);
    assert.equal(h.get("suggestions-dropdown").hidden, true);
    assert.equal(h.requests.length, 1, "the replacement request must still be debounced");
    h.requests[0].resolve({ suggestions: ["Old greeting"] });
    await flushResponses();
    assert.equal(h.get("suggestions-dropdown").hidden, true);
    assert.deepEqual(visibleSuggestions(h), []);
    h.timers.advance(300);
    assert.equal(h.requests.length, 2);
    assert.equal(h.requests[1].body.text, "Welcome");
});

test("deleting below three characters aborts and never schedules a replacement", async () => {
    const h = setup("suggestions.js");
    h.type("Hello");
    h.timers.advance(300);
    h.type("He");
    assert.equal(h.requests[0].signal.aborted, true);
    h.requests[0].resolve({ suggestions: ["Hello again"] });
    await flushResponses();
    h.timers.advance(1000);
    assert.equal(h.requests.length, 1);
    assert.equal(h.get("suggestions-dropdown").hidden, true);
});

test("an older response or error cannot replace newer suggestions", async () => {
    for (const failOldRequest of [false, true]) {
        const h = setup("suggestions.js");
        h.type("Hello");
        h.timers.advance(300);
        h.type("Welcome");
        h.timers.advance(300);
        h.requests[1].resolve({ suggestions: ["Welcome Team!"] });
        await flushResponses();
        if (failOldRequest) h.requests[0].reject(new Error("Old network error"));
        else h.requests[0].resolve({ suggestions: ["Old greeting"] });
        await flushResponses();
        assert.deepEqual(visibleSuggestions(h), ["Welcome Team!"]);
        assert.equal(h.get("suggestions-dropdown").hidden, false);
    }
});

test("clicking a suggestion fills literal text and cancels follow-up work", async () => {
    const h = setup("suggestions.js");
    const text = '<img src=x onerror="alert(1)">';
    h.type("Hello");
    h.timers.advance(300);
    h.requests[0].resolve({ suggestions: [null, 42, "Привет", text] });
    await flushResponses();
    const dropdown = h.get("suggestions-dropdown");
    assert.equal(dropdown.children.length, 1);
    const button = dropdown.children[0];
    assert.equal(button.tagName, "button");
    assert.equal(button.type, "button");
    assert.equal(button.textContent, text);
    assert.equal(button.childElementCount, 0, "HTML-like input must remain plain text");
    button.emit("click");
    assert.equal(h.input.value, text);
    assert.equal(h.fills[0].text, text);
    assert.equal(dropdown.hidden, true);
    assert.equal(h.get("suggestions-status").textContent, "");
    h.timers.advance(1000);
    assert.equal(h.requests.length, 1, "selection must not leave a queued suggestion request");
});

test("Escape and generation cancel pending suggestions", async () => {
    for (const event of ["escape", "generate"]) {
        const h = setup("suggestions.js");
        h.type("Hello");
        h.timers.advance(300);
        if (event === "escape") h.input.emit("keydown", { key: "Escape" });
        else h.input.emit("studio:generating");
        assert.equal(h.requests[0].signal.aborted, true);
        h.requests[0].resolve({ suggestions: ["Too late"] });
        await flushResponses();
        assert.equal(h.get("suggestions-dropdown").hidden, true);
    }
});

test("current suggestion failures are readable and clear on edit", async () => {
    const h = setup("suggestions.js");
    h.type("Hello");
    h.timers.advance(300);
    h.requests[0].reject(new Error("Service unavailable"));
    await flushResponses();
    assert.equal(h.get("suggestions-dropdown").textContent, "Service unavailable");
    assert.equal(h.get("suggestions-status").textContent, "Service unavailable");
    h.type("New input");
    assert.equal(h.get("suggestions-dropdown").hidden, true);
    assert.equal(h.get("suggestions-status").textContent, "");
});

test("stale recommendations cannot select or highlight a banner", async () => {
    const h = setup("recommendations.js");
    const button = h.get("ai-recommend-btn");
    h.type("WELCOME");
    button.emit("click");
    assert.equal(h.requests[0].url, "/api/recommend-banner");
    assert.equal(button.disabled, true);
    h.type("A new message");
    assert.equal(h.requests[0].signal.aborted, true);
    assert.equal(button.disabled, false);
    h.requests[0].resolve({ recommended: "shadow", reasoning: "Old recommendation" });
    await flushResponses();
    assert.equal(h.get("recommendation-result").hidden, true);
    assert.deepEqual(h.selections, []);
    assert.equal(h.labels.get("shadow").classList.contains("ai-pick"), false);
    button.emit("click");
    h.requests[1].resolve({ recommended: "standard", reasoning: "Readable for longer text", alternatives: [] });
    await flushResponses();
    assert.deepEqual(h.selections, ["standard"]);
    assert.equal(h.labels.get("standard").classList.contains("ai-pick"), true);
    assert.equal(button.disabled, false);
    h.type("Next message");
    assert.equal(h.labels.get("standard").classList.contains("ai-pick"), false);
});

test("manual banner selection cancels an in-flight recommendation", async () => {
    const h = setup("recommendations.js");
    h.type("WELCOME");
    h.get("ai-recommend-btn").emit("click");
    h.form.emit("change", { target: { name: "banner", value: "thinkertoy" } });
    assert.equal(h.requests[0].signal.aborted, true);
    assert.equal(h.get("ai-recommend-btn").disabled, false);
    h.requests[0].resolve({ recommended: "shadow", reasoning: "Old recommendation" });
    await flushResponses();
    assert.deepEqual(h.selections, []);
    assert.equal(h.get("recommendation-result").hidden, true);
});

test("closing or editing variations cancels requests and ignores late results", async () => {
    for (const action of ["close", "edit"]) {
        const h = setup("variations.js");
        h.type("Hello");
        h.get("get-variations-btn").emit("click");
        assert.equal(h.requests[0].url, "/api/variations");
        assert.equal(h.get("variations-modal").open, true);
        if (action === "close") h.get("variations-close").emit("click");
        else h.type("Welcome");
        assert.equal(h.requests[0].signal.aborted, true);
        assert.equal(h.get("variations-modal").open, false);
        assert.equal(h.get("get-variations-btn").disabled, false);
        assert.equal(h.get("variations-list").getAttribute("aria-busy"), null);
        h.requests[0].resolve({ variations: [{ text: "OLD!", description: "Old idea", suggested_banner: "shadow" }] });
        await flushResponses();
        assert.equal(h.get("variations-modal").open, false);
        assert.equal(h.get("variations-list").querySelectorAll("button").length, 0);
        assert.deepEqual(h.fills, []);
    }
});

test("choosing a variation restores its text and banner safely", async () => {
    const h = setup("variations.js");
    h.type("Hello");
    h.get("get-variations-btn").emit("click");
    const text = "<Hello>";
    h.requests[0].resolve({ variations: [{ text, description: "<b>A greeting</b>", suggested_banner: "shadow" }] });
    await flushResponses();
    const list = h.get("variations-list");
    assert.equal(list.querySelector("h3").textContent, text);
    assert.equal(list.querySelector("p").textContent, "<b>A greeting</b>");
    list.querySelector("button").emit("click");
    assert.equal(h.input.value, text);
    assert.deepEqual(h.selections, ["shadow"]);
    assert.equal(h.get("variations-modal").open, false);
});

test("suggestions support keyboard navigation, wrapping and Escape", async () => {
    const h = setup("suggestions.js");
    h.type("Hello");
    h.timers.advance(300);
    h.requests[0].resolve({ suggestions: ["Hello one", "Hello two", "Hello three"] });
    await flushResponses();
    const dropdown = h.get("suggestions-dropdown");
    const buttons = dropdown.querySelectorAll("button");
    let prevented = 0;
    h.input.emit("keydown", { key: "ArrowDown", altKey: true, preventDefault() { prevented += 1; } });
    assert.equal(h.document.activeElement, buttons[0]);
    assert.equal(prevented, 1);
    for (const [key, index] of [["ArrowUp", 2], ["ArrowDown", 0], ["End", 2], ["Home", 0]]) {
        dropdown.emit("keydown", { key });
        assert.equal(h.document.activeElement, buttons[index]);
    }
    dropdown.emit("keydown", { key: "Escape" });
    assert.equal(h.document.activeElement, h.input);
    assert.equal(dropdown.hidden, true);
});

test("focus and pointer departure cancel debounced and in-flight suggestions", async () => {
    for (const event of ["focusin", "pointerdown"]) {
        for (const started of [false, true]) {
            const h = setup("suggestions.js");
            h.type("Hello");
            if (started) h.timers.advance(300);
            h.document.emit(event, { target: h.get("another-control") });
            if (started) {
                assert.equal(h.requests[0].signal.aborted, true);
                h.requests[0].resolve({ suggestions: ["Too late"] });
                await flushResponses();
            }
            h.timers.advance(1000);
            assert.equal(h.requests.length, Number(started));
            assert.equal(h.get("suggestions-dropdown").hidden, true);
        }
    }
});

test("invalid recommendation payload is readable and does not select a banner", async () => {
    for (const payload of [null, {}, { recommended: "unknown", reasoning: "Bad" }, { recommended: "shadow", reasoning: 7 }]) {
        const h = setup("recommendations.js");
        h.type("WELCOME");
        h.get("ai-recommend-btn").emit("click");
        h.requests[0].resolve(payload);
        await flushResponses();
        assert.match(h.get("recommendation-result").textContent, /Не удалось подобрать/);
        assert.equal(h.get("ai-recommend-btn").disabled, false);
        assert.deepEqual(h.selections, []);
    }
});

test("failed variations can retry without reopening or leaking busy state", async () => {
    const h = setup("variations.js");
    h.type("Hello");
    h.get("get-variations-btn").emit("click");
    h.requests[0].reject(new Error("Temporarily unavailable"));
    await flushResponses();
    assert.match(h.get("variations-list").textContent, /Temporarily unavailable/);
    assert.equal(h.get("get-variations-btn").disabled, false);
    h.get("variations-list").querySelector("button").emit("click");
    assert.equal(h.requests.length, 2);
    assert.equal(h.requests[1].body.text, "Hello");
    h.requests[1].resolve({ variations: [{ text: "Hello!", description: "A greeting", suggested_banner: "standard" }] });
    await flushResponses();
    assert.equal(h.get("variations-modal").open, true);
    assert.equal(h.get("variations-list").getAttribute("aria-busy"), null);
    assert.equal(h.get("variations-list").querySelector("h3").textContent, "Hello!");
});

test("request sends JSON with same-origin credentials and propagates server errors", async () => {
    const h = setupApp();
    const pending = h.window.SymbolStudio.request("/api/suggest", { text: "Hello" });
    assert.equal(h.requests[0].method, "POST");
    assert.equal(h.requests[0].credentials, "same-origin");
    assert.equal(h.requests[0].headers.Accept, "application/json");
    assert.equal(h.requests[0].headers["Content-Type"], "application/json");
    assert.deepEqual(JSON.parse(h.requests[0].body), { text: "Hello" });
    h.respond({ error: "AI временно недоступен" }, { ok: false });
    await assert.rejects(pending, /AI временно недоступен/);
    h.timers.advance(12000);
    assert.equal(h.requests[0].signal.aborted, false, "completed requests must clear their timeout");
});

test("request converts invalid JSON, empty error responses and network rejection to readable errors", async () => {
    for (const scenario of ["json", "http", "network"]) {
        const h = setupApp();
        const pending = h.window.SymbolStudio.request("/api/suggest", { text: "Hello" });
        let message;
        if (scenario === "json") {
            h.respond(null, { jsonError: new SyntaxError("Unexpected end") });
            message = /некорректный ответ/;
        } else if (scenario === "http") {
            h.respond({}, { ok: false });
            message = /временно недоступен/;
        } else {
            h.requests[0].reject(new TypeError("Failed to fetch"));
            message = /Проверь подключение/;
        }
        await assert.rejects(pending, message);
    }
});

test("request deadline aborts stalled fetch and also covers stalled JSON parsing", async () => {
    for (const parsing of [false, true]) {
        const h = setupApp({ abortFetch: true });
        const pending = h.window.SymbolStudio.request("/api/suggest", { text: "Hello" });
        if (parsing) {
            h.requests[0].resolve({
                ok: true,
                json: () => new Promise((_, reject) => h.requests[0].signal.addEventListener("abort", () => reject(new DOMException("Aborted body", "AbortError")), { once: true })),
            });
            await flushResponses();
        }
        h.timers.advance(11999);
        assert.equal(h.requests[0].signal.aborted, false);
        h.timers.advance(1);
        await assert.rejects(pending, /слишком долго/);
        assert.equal(h.requests[0].signal.aborted, true);
    }
});

test("caller cancellation remains an AbortError and detaches after completion", async () => {
    const h = setupApp({ abortFetch: true });
    const caller = new AbortController();
    const pending = h.window.SymbolStudio.request("/api/suggest", { text: "Hello" }, caller.signal);
    caller.abort();
    await assert.rejects(pending, { name: "AbortError" });
    const second = new AbortController();
    const completed = h.window.SymbolStudio.request("/api/suggest", { text: "Hello" }, second.signal);
    h.respond({ suggestions: ["Hello!"] });
    await completed;
    second.abort();
    assert.equal(h.requests[1].signal.aborted, false, "a completed request must detach its caller listener");
});

test("generation preserves spaces and newlines and posts form data", async () => {
    const h = setupApp();
    const text = "  A & B\n <Hello> + ";
    h.type(text);
    const pending = h.form.emit("submit");
    assert.equal(h.get("generate-btn").disabled, true);
    assert.equal(h.requests[0].url, "/symbol-art");
    assert.match(h.requests[0].headers["Content-Type"], /application\/x-www-form-urlencoded/);
    const values = new URLSearchParams(h.requests[0].body);
    assert.equal(values.get("text"), text);
    assert.equal(values.get("banner"), "standard");
    h.respond({ result: "  ART\n" });
    await pending;
    assert.equal(h.get("ascii-result").textContent, "  ART\n");
    assert.equal(h.get("result-details").textContent, "5 × 1 / UTF-8");
    assert.equal(h.get("generate-btn").disabled, false);
    assert.equal(h.get("copy-result").disabled, false);
});

test("ASCII validation accepts whitespace and rejects unsupported or oversized input before fetching", async () => {
    const h = setupApp();
    for (const valid of [" ", "\n", "\r\n", "x".repeat(1000)]) assert.equal(h.window.SymbolStudio.validate(valid), "");
    for (const invalid of ["", "\t", "Привет", "😀", "x".repeat(1001)]) {
        h.type(invalid);
        await h.form.emit("submit");
        assert.equal(h.requests.length, 0);
        assert.equal(h.input.getAttribute("aria-invalid"), "true");
        assert.equal(h.get("form-status").classList.contains("is-error"), true);
    }
    h.type("Valid");
    assert.equal(h.input.getAttribute("aria-invalid"), null);
    assert.equal(h.get("form-status").textContent, "");
});

test("editing during generation ignores late success and keeps a completed art marked stale", async () => {
    const h = setupApp({ text: "Original", result: "ORIGINAL ART\n" });
    h.type("First");
    const first = h.form.emit("submit");
    h.type("Second");
    assert.equal(h.requests[0].signal.aborted, true);
    assert.equal(h.get("generate-btn").disabled, false);
    h.respond({ result: "STALE ART\n" });
    await first;
    assert.equal(h.get("ascii-result").textContent, "ORIGINAL ART\n");
    assert.equal(h.get("copy-result").disabled, true);
    assert.equal(h.get("download-result").disabled, true);
    assert.match(h.get("result-status").textContent, /Создай арт ещё раз/);
    const second = h.form.emit("submit");
    h.respond({ result: "SECOND ART\n" });
    await second;
    assert.equal(h.get("ascii-result").textContent, "SECOND ART\n");
    assert.equal(h.get("copy-result").disabled, false);
});

test("older generation failures cannot overwrite a newer successful generation", async () => {
    const h = setupApp();
    h.type("First");
    const first = h.form.emit("submit");
    h.type("Second");
    const second = h.form.emit("submit");
    h.respond({ result: "NEW ART\n" }, { index: 1 });
    await second;
    h.requests[0].reject(new TypeError("Old failure"));
    await first;
    assert.equal(h.get("ascii-result").textContent, "NEW ART\n");
    assert.match(h.get("form-status").textContent, /^Готово/);
    assert.equal(h.get("generate-btn").disabled, false);
});

test("malformed generation response releases busy state and allows retry", async () => {
    const h = setupApp();
    h.type("Hello");
    const first = h.form.emit("submit");
    h.respond({ result: null });
    await first;
    assert.match(h.get("form-status").textContent, /Не удалось прочитать арт/);
    assert.equal(h.get("generate-btn").disabled, false);
    assert.equal(h.get("ascii-result").hidden, true);
    const retry = h.form.emit("submit");
    h.respond({ result: "HELLO\n" });
    await retry;
    assert.equal(h.get("ascii-result").textContent, "HELLO\n");
});

test("history tolerates malformed, oversized, blocked and invalid storage", () => {
    for (const options of [
        { history: "{" }, { history: "null" }, { history: "{}" }, { history: "x".repeat(16001) },
        { storageReadError: true },
        { history: JSON.stringify([null, 42, {}, { text: "", banner: "standard" }, { text: "Привет", banner: "standard" }, { text: "Hello", banner: "unknown" }]) },
    ]) {
        const h = setupApp(options);
        assert.equal(h.get("history-panel").hidden, true);
        assert.equal(h.get("history-list").childElementCount, 0);
        assert.equal(h.get("generate-btn").disabled, false);
    }
});

test("history is bounded, deduplicates and renders stored text literally", async () => {
    const entries = Array.from({ length: 9 }, (_, index) => ({ text: index ? "Idea " + index : "<img src=x>", banner: "shadow" }));
    const h = setupApp({ history: JSON.stringify(entries) });
    assert.equal(h.get("history-list").childElementCount, 6);
    const item = h.get("history-list").firstElementChild;
    assert.equal(item.firstElementChild.textContent, "<img src=x>");
    assert.equal(item.firstElementChild.childElementCount, 0);
    await item.emit("click");
    assert.equal(h.input.value, "<img src=x>");
    assert.equal(h.window.SymbolStudio.selectedBanner(), "shadow");
    const pending = h.form.emit("submit");
    h.respond({ result: "ART\n" });
    await pending;
    const saved = JSON.parse(h.storage.get("symbol-web.history.v1"));
    assert.equal(saved.length, 6);
    assert.equal(saved.filter((entry) => entry.text === "<img src=x>").length, 1);
});

test("full or blocked storage leaves generation and in-memory history usable", async () => {
    const h = setupApp({ storageReadError: true, storageWriteError: true });
    h.type("Hello");
    const pending = h.form.emit("submit");
    h.respond({ result: "HELLO\n" });
    await pending;
    assert.equal(h.get("ascii-result").textContent, "HELLO\n");
    assert.equal(h.get("history-list").childElementCount, 1);
    assert.equal(h.get("history-panel").hidden, false);
    await h.get("clear-history").emit("click");
    assert.equal(h.get("history-panel").hidden, true);
    assert.equal(h.get("history-list").childElementCount, 0);
});

test("clipboard denial falls back to selecting the art", async () => {
    const h = setupApp({ text: "Hello", result: "HELLO\n", clipboard: { writeText: () => Promise.reject(new Error("Denied")) } });
    await h.get("copy-result").emit("click");
    assert.deepEqual(h.selected, [h.get("ascii-result")]);
    assert.equal(h.document.activeElement, h.get("ascii-result"));
    assert.match(h.get("result-status").textContent, /Ctrl\+C/);
});

test("clipboard success copies the complete art including whitespace", async () => {
    const copied = [];
    const h = setupApp({ text: "Hello", result: "  HELLO  \n\n", clipboard: { writeText: async (text) => { copied.push(text); } } });
    await h.get("copy-result").emit("click");
    assert.deepEqual(copied, ["  HELLO  \n\n"]);
    assert.deepEqual(h.selected, []);
    assert.match(h.get("result-status").textContent, /^Скопировано/);
});

test("clipboard fallback failure remains readable instead of rejecting the click", async () => {
    const h = setupApp({ text: "Hello", result: "HELLO\n", selectionUnavailable: true });
    await h.get("copy-result").emit("click");
    assert.match(h.get("result-status").textContent, /Не удалось скопировать/);
    assert.equal(h.get("download-result").disabled, false);
});

test("clipboard completion after an edit cannot replace stale-result feedback", async () => {
    for (const failure of [false, true]) {
        let resolveCopy;
        let rejectCopy;
        const h = setupApp({
            text: "Hello", result: "HELLO\n",
            clipboard: { writeText: () => new Promise((resolve, reject) => { resolveCopy = resolve; rejectCopy = reject; }) },
        });
        const copying = h.get("copy-result").emit("click");
        h.type("Changed");
        if (failure) rejectCopy(new Error("Denied"));
        else resolveCopy();
        await copying;
        assert.match(h.get("result-status").textContent, /Создай арт ещё раз/);
        assert.deepEqual(h.selected, []);
    }
});

test("download creates a text file and revokes its object URL", async () => {
    const h = setupApp({ text: "Hello", banner: "shadow", result: "HELLO\n" });
    await h.get("download-result").emit("click");
    assert.equal(await h.downloads[0].text(), "HELLO\n");
    assert.equal(h.downloads[0].type, "text/plain;charset=utf-8");
    assert.equal(h.links[0].download, "symbol-web-shadow.txt");
    assert.equal(h.links[0].href, "blob:test/1");
    assert.equal(h.links[0].clickCount, 1);
    assert.equal(h.document.body.childElementCount, 0, "temporary links must be removed");
    h.timers.advance(1000);
    assert.deepEqual(h.revoked, ["blob:test/1"]);
    assert.match(h.get("result-status").textContent, /готов к сохранению/);
});

test("download failures show a readable error and release links and object URLs", async () => {
    for (const options of [{ objectURLError: true }, { linkClickError: true }]) {
        const h = setupApp({ text: "Hello", result: "HELLO\n", ...options });
        await h.get("download-result").emit("click");
        assert.match(h.get("result-status").textContent, /Не удалось скачать/);
        assert.equal(h.document.body.childElementCount, 0);
        h.timers.advance(1000);
        assert.equal(h.revoked.length, h.downloads.length);
    }
});
