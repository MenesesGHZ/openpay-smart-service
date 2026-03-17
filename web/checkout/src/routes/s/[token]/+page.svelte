<script>
  import { onMount } from 'svelte';
  import {
    PUBLIC_OPENPAY_MERCHANT_ID,
    PUBLIC_OPENPAY_PUBLIC_KEY,
    PUBLIC_OPENPAY_SANDBOX,
  } from '$env/static/public';

  export let data;
  const { token, info } = data;

  // ── State ─────────────────────────────────────────────────────────────────
  let cardNumber    = '';
  let expiry        = '';
  let cvc           = '';
  let nameOnCard    = info.memberName || '';
  let email         = info.memberEmail || '';

  let state         = 'idle'; // idle | processing | success | error
  let errorMsg      = '';
  let deviceSessionId = '';
  let openpayReady  = false;
  let cardFocused   = false;

  // ── Derived ───────────────────────────────────────────────────────────────
  $: alreadyCompleted = info.status === 'completed';
  $: alreadyExpired   = info.status === 'expired' || info.status === 'cancelled';

  $: formattedAmount = (() => {
    const major = (info.amount / 100).toLocaleString('es-MX', {
      style: 'currency', currency: info.currency || 'MXN', minimumFractionDigits: 2
    });
    return major;
  })();

  $: intervalLabel = (() => {
    if (!info.repeatUnit) return '';
    const every = info.repeatEvery || 1;
    const unit  = info.repeatUnit;
    if (every === 1) return `/ ${unit}`;
    return `/ every ${every} ${unit}s`;
  })();

  $: currencyCode = (info.currency || 'MXN').toUpperCase();

  // Use the explicit description from the link if set, otherwise auto-generate
  // a human-readable billing cadence string (e.g. "Billed monthly · Cancel anytime").
  $: planDescription = (() => {
    if (info.description) return info.description;
    if (!info.repeatUnit) return '';
    const every = info.repeatEvery || 1;
    const unit  = info.repeatUnit.toLowerCase();
    const unitLabel = every === 1
      ? unit
      : `${every} ${unit}s`;
    const billingLine = `Billed ${every === 1 ? unit + 'ly' : `every ${unitLabel}`}`;
    return `${billingLine} · Cancel anytime`;
  })();

  $: cardBrand = (() => {
    const n = cardNumber.replace(/\s/g, '');
    if (/^4/.test(n))          return 'visa';
    if (/^5[1-5]/.test(n))     return 'mastercard';
    if (/^3[47]/.test(n))      return 'amex';
    return 'unknown';
  })();

  // ── OpenPay init ──────────────────────────────────────────────────────────
  onMount(() => {
    // Poll until the OpenPay global is available (async script load)
    const interval = setInterval(() => {
      if (typeof window.OpenPay !== 'undefined') {
        clearInterval(interval);
        window.OpenPay.setId(PUBLIC_OPENPAY_MERCHANT_ID);
        window.OpenPay.setApiKey(PUBLIC_OPENPAY_PUBLIC_KEY);
        window.OpenPay.setSandboxMode(PUBLIC_OPENPAY_SANDBOX === 'true');
        deviceSessionId = window.OpenPay.deviceData.setup();
        openpayReady = true;
      }
    }, 100);
    return () => clearInterval(interval);
  });

  // ── Input formatting ──────────────────────────────────────────────────────
  function onCardNumberInput(e) {
    const raw    = e.target.value.replace(/\D/g, '').slice(0, 16);
    const chunks = raw.match(/.{1,4}/g) || [];
    cardNumber   = chunks.join('  ');
    e.target.value = cardNumber;
  }

  function onExpiryInput(e) {
    const raw = e.target.value.replace(/\D/g, '').slice(0, 4);
    expiry = raw.length > 2 ? raw.slice(0, 2) + ' / ' + raw.slice(2) : raw;
    e.target.value = expiry;
  }

  function onExpiryKeydown(e) {
    // Allow backspace to delete the separator cleanly
    if (e.key === 'Backspace' && expiry.endsWith(' / ')) {
      e.preventDefault();
      expiry = expiry.slice(0, -3);
    }
  }

  function onCvcInput(e) {
    cvc = e.target.value.replace(/\D/g, '').slice(0, 4);
    e.target.value = cvc;
  }

  // ── Submit ────────────────────────────────────────────────────────────────
  async function submit() {
    if (state === 'processing') return;
    errorMsg = '';

    // Basic validation
    const rawCard = cardNumber.replace(/\s/g, '');
    if (rawCard.length < 15) { errorMsg = 'Please enter a valid card number.'; return; }
    if (!expiry.includes('/')) { errorMsg = 'Please enter the card expiry date.'; return; }
    if (cvc.length < 3)      { errorMsg = 'Please enter the CVC.'; return; }
    if (!nameOnCard.trim())  { errorMsg = 'Please enter the name on the card.'; return; }

    const [mm, yy] = expiry.split('/').map(s => s.trim());

    if (!openpayReady) {
      errorMsg = 'Payment processor is still loading. Please try again in a moment.';
      return;
    }

    state = 'processing';

    // Step 1 – tokenize card via OpenPay JS SDK
    const tokenResult = await new Promise((resolve) => {
      window.OpenPay.token.create(
        {
          card_number:      rawCard,
          holder_name:      nameOnCard,
          expiration_year:  yy,
          expiration_month: mm,
          cvv2:             cvc,
        },
        (res) => resolve({ ok: true,  tokenId: res.data.id }),
        (err) => resolve({ ok: false, message: err.data?.description || 'Card tokenization failed.' })
      );
    });

    if (!tokenResult.ok) {
      state    = 'error';
      errorMsg = tokenResult.message;
      return;
    }

    // Step 2 – redeem subscription link
    try {
      const res = await fetch(`/api/redeem/${token}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          token_id:          tokenResult.tokenId,
          device_session_id: deviceSessionId,
        }),
      });

      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.message || `Server error ${res.status}`);
      }

      state = 'success';
    } catch (err) {
      state    = 'error';
      errorMsg = err.message || 'Something went wrong. Please try again.';
    }
  }
</script>

<svelte:head>
  <title>{info.planName ? `Subscribe to ${info.planName}` : 'Checkout'}</title>
  <script src="https://js.openpay.mx/openpay.v1.min.js"></script>
  <script src="https://js.openpay.mx/openpay-data.v1.min.js"></script>
</svelte:head>

<!-- ── Already completed / expired ──────────────────────────────────────── -->
{#if alreadyCompleted}
  <div class="solo-wrap">
    <div class="solo-card">
      <div class="check-icon success">
        <svg viewBox="0 0 24 24" fill="none">
          <circle cx="12" cy="12" r="12" fill="#00b574"/>
          <path d="M7 12l3.5 3.5L17 8" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </div>
      <h2>Already subscribed</h2>
      <p>This payment link has already been used to set up a subscription.</p>
    </div>
  </div>

{:else if alreadyExpired}
  <div class="solo-wrap">
    <div class="solo-card">
      <div class="check-icon error">
        <svg viewBox="0 0 24 24" fill="none">
          <circle cx="12" cy="12" r="12" fill="#df1b41"/>
          <path d="M8 8l8 8M16 8l-8 8" stroke="#fff" stroke-width="2" stroke-linecap="round"/>
        </svg>
      </div>
      <h2>Link expired</h2>
      <p>This payment link has expired or been cancelled. Please request a new one.</p>
    </div>
  </div>

<!-- ── Success screen ─────────────────────────────────────────────────────── -->
{:else if state === 'success'}
  <div class="solo-wrap">
    <div class="solo-card">
      <div class="check-icon success">
        <svg viewBox="0 0 24 24" fill="none">
          <circle cx="12" cy="12" r="12" fill="#00b574"/>
          <path d="M7 12l3.5 3.5L17 8" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </div>
      <h2>You're subscribed!</h2>
      <p>Your subscription to <strong>{info.planName}</strong> is now active. A confirmation will be sent to <strong>{email}</strong>.</p>
      <p class="sub-detail">{formattedAmount} {intervalLabel}</p>
    </div>
  </div>

<!-- ── Checkout form ──────────────────────────────────────────────────────── -->
{:else}
  <div class="layout">
    <!-- Left panel — order summary -->
    <aside class="summary">
      <div class="summary-inner">
        <div class="merchant">
          {#if info.logoUrl}
            <img class="merchant-logo-img" src={info.logoUrl} alt={info.tenantName || 'Merchant logo'} />
          {:else}
            <div class="merchant-logo" aria-hidden="true">
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
                <path d="M2.5 7l3 3 6-6" stroke="white" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
            </div>
          {/if}
          <span class="merchant-name">{info.tenantName || 'OpenPay'}</span>
        </div>

        <div class="plan-info">
          <p class="plan-label">Subscribe to</p>
          <h1 class="plan-name">{info.planName}</h1>
          {#if planDescription}
            <p class="plan-description">{planDescription}</p>
          {/if}
          <p class="plan-price">
            <span class="price-amount">{formattedAmount}</span>
            <span class="price-currency">{currencyCode}</span>
            <span class="price-interval">{intervalLabel}</span>
          </p>
        </div>

        <div class="line-items">
          <div class="summary-line">
            <span>Subtotal</span>
            <span>{formattedAmount}</span>
          </div>
          <div class="summary-line total">
            <span>Total due today</span>
            <span>{formattedAmount}</span>
          </div>
        </div>

        <div class="summary-footer">
          <div class="powered-by">
            <svg class="op-icon" viewBox="0 0 16 16" fill="none">
              <rect width="16" height="16" rx="4" fill="var(--brand)"/>
              <path d="M4 8l2.75 2.75L12 5.5" stroke="white" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            <span>Powered by <strong>OpenPay</strong></span>
          </div>
        </div>
      </div>
    </aside>

    <!-- Right panel — payment form -->
    <main class="form-panel">
      <div class="form-inner">
        <!-- Contact info -->
        <section class="form-section">
          <h2 class="section-label">Contact information</h2>
          <div class="field">
            <label for="email">Email</label>
            <input
              id="email"
              type="email"
              bind:value={email}
              placeholder="you@example.com"
              autocomplete="email"
            />
          </div>
        </section>

        <!-- Payment details -->
        <section class="form-section">
          <h2 class="section-label">Card information</h2>

          <!-- Stripe-style grouped card input -->
          <div class="card-group" class:cg-focused={cardFocused}>
            <!-- Row 1: card number + card brand icons -->
            <div class="cg-row cg-number">
              <input
                id="cardNumber"
                type="text"
                inputmode="numeric"
                placeholder="1234 1234 1234 1234"
                autocomplete="cc-number"
                value={cardNumber}
                on:input={onCardNumberInput}
                on:focus={() => cardFocused = true}
                on:blur={() => cardFocused = false}
              />
              <span class="ci-group">
                <svg class="ci" class:ci-dim={cardBrand !== 'unknown' && cardBrand !== 'visa'} viewBox="0 0 38 24" fill="none">
                  <rect width="38" height="24" rx="3" fill="#fff" stroke="#e4e4e7"/>
                  <text x="19" y="16" text-anchor="middle" font-family="Arial" font-size="10" font-weight="800" fill="#1A1F71">VISA</text>
                </svg>
                <svg class="ci" class:ci-dim={cardBrand !== 'unknown' && cardBrand !== 'mastercard'} viewBox="0 0 38 24" fill="none">
                  <rect width="38" height="24" rx="3" fill="#fff" stroke="#e4e4e7"/>
                  <circle cx="15" cy="12" r="7" fill="#EB001B"/>
                  <circle cx="23" cy="12" r="7" fill="#F79E1B"/>
                  <path d="M19 6.8a7 7 0 010 10.4A7 7 0 0119 6.8z" fill="#FF5F00"/>
                </svg>
                <svg class="ci" class:ci-dim={cardBrand !== 'unknown' && cardBrand !== 'amex'} viewBox="0 0 38 24" fill="none">
                  <rect width="38" height="24" rx="3" fill="#2E77BC"/>
                  <text x="19" y="16" text-anchor="middle" font-family="Arial" font-size="8" font-weight="700" fill="#fff">AMEX</text>
                </svg>
              </span>
            </div>
            <!-- Row 2: expiry | separator | CVC -->
            <div class="cg-row cg-bottom">
              <input
                class="cg-half"
                id="expiry"
                type="text"
                inputmode="numeric"
                placeholder="MM / YY"
                autocomplete="cc-exp"
                value={expiry}
                on:input={onExpiryInput}
                on:keydown={onExpiryKeydown}
                on:focus={() => cardFocused = true}
                on:blur={() => cardFocused = false}
              />
              <span class="cg-sep"></span>
              <input
                class="cg-half"
                id="cvc"
                type="text"
                inputmode="numeric"
                placeholder="CVC"
                autocomplete="cc-csc"
                value={cvc}
                on:input={onCvcInput}
                on:focus={() => cardFocused = true}
                on:blur={() => cardFocused = false}
              />
            </div>
          </div>

          <!-- Cardholder name — standalone field below group -->
          <div class="field" style="margin-top:.875rem;">
            <label for="nameOnCard">Cardholder name</label>
            <input
              id="nameOnCard"
              type="text"
              bind:value={nameOnCard}
              placeholder="Full name on card"
              autocomplete="cc-name"
            />
          </div>
        </section>

        <!-- Error message -->
        {#if errorMsg}
          <div class="error-banner" role="alert">
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <circle cx="8" cy="8" r="7.5" stroke="#df1b41"/>
              <path d="M8 4.5v4M8 10.5v1" stroke="#df1b41" stroke-width="1.5" stroke-linecap="round"/>
            </svg>
            {errorMsg}
          </div>
        {/if}

        <!-- Submit button -->
        <button
          class="btn-subscribe"
          on:click={submit}
          disabled={state === 'processing'}
          aria-live="polite"
        >
          {#if state === 'processing'}
            <span class="spinner"></span>
            Processing…
          {:else}
            Subscribe {formattedAmount} {intervalLabel}
          {/if}
        </button>

        <!-- Terms -->
        <p class="terms">
          By confirming your subscription, you allow OpenPay to charge your card for this payment and future payments in accordance with their terms.
        </p>

        <!-- Powered by (mobile only) -->
        <div class="powered-by-mobile">
          <svg class="op-icon" viewBox="0 0 16 16" fill="none">
            <rect width="16" height="16" rx="4" fill="var(--brand)"/>
            <path d="M4 8l2.75 2.75L12 5.5" stroke="white" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
          <span>Powered by <strong>OpenPay</strong></span>
        </div>
      </div>
    </main>
  </div>
{/if}

<style>
  /* ── Layout ──────────────────────────────────────────────────────────────── */
  .layout {
    height: 100vh;
    overflow: hidden;
    display: grid;
    grid-template-columns: 1fr 1fr;
  }

  /* ── Summary — light left panel ─────────────────────────────────────────── */
  .summary {
    background: var(--panel-bg);
    display: flex;
    justify-content: flex-end;
    position: relative;
    z-index: 1;
    box-shadow: 4px 0 16px rgba(0,0,0,.06);
    height: 100%;
    overflow-y: auto;
  }
  .summary-inner {
    max-width: 440px;
    width: 100%;
    padding: 4rem 3rem 3rem 2.5rem;
    display: flex;
    flex-direction: column;
  }

  /* merchant header */
  .merchant {
    display: flex;
    align-items: center;
    gap: .625rem;
    margin-bottom: 2.5rem;
  }
  .merchant-logo {
    width: 32px; height: 32px;
    background: var(--brand);
    border-radius: 8px;
    display: flex; align-items: center; justify-content: center;
    flex-shrink: 0;
    box-shadow: 0 1px 3px rgba(0,0,0,.12);
  }
  .merchant-logo svg { display: block; }
  .merchant-logo-img {
    width: 32px; height: 32px;
    border-radius: 8px;
    object-fit: cover;
    flex-shrink: 0;
    display: block;
    box-shadow: 0 1px 3px rgba(0,0,0,.12);
  }
  .merchant-name {
    font-size: .9375rem;
    font-weight: 600;
    color: var(--panel-text);
    letter-spacing: -.02em;
  }

  /* plan block */
  .plan-label {
    font-size: .6875rem;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: .08em;
    color: var(--panel-muted);
    margin-bottom: .5rem;
  }
  .plan-name {
    font-size: 1.125rem;
    font-weight: 600;
    color: var(--panel-text);
    margin-bottom: .375rem;
    letter-spacing: -.02em;
    line-height: 1.35;
  }
  .plan-description {
    font-size: .8125rem;
    color: var(--panel-muted);
    letter-spacing: -.005em;
    margin-bottom: 1.5rem;
    line-height: 1.55;
  }
  .plan-price {
    display: flex;
    align-items: baseline;
    gap: .3rem;
    flex-wrap: wrap;
  }
  .price-amount {
    font-size: 2.5rem;
    font-weight: 700;
    color: var(--panel-text);
    letter-spacing: -.04em;
    line-height: 1;
  }
  .price-currency {
    font-size: .6875rem;
    font-weight: 500;
    color: var(--panel-muted);
    letter-spacing: .06em;
    text-transform: uppercase;
    background: var(--panel-subtle);
    border: 1px solid var(--panel-border);
    border-radius: 4px;
    padding: .15rem .4rem;
    align-self: flex-end;
    margin-bottom: .3rem;
    font-family: var(--font-mono);
  }
  .price-interval {
    font-size: .8125rem;
    color: var(--panel-muted);
    font-weight: 400;
    letter-spacing: 0;
  }

  /* line items */
  .line-items {
    border-top: 1px solid var(--panel-border);
    padding-top: 1.25rem;
    margin-top: 1.75rem;
  }
  .summary-line {
    display: flex;
    justify-content: space-between;
    font-size: .8125rem;
    color: var(--panel-muted);
    margin-bottom: .5rem;
    letter-spacing: -.005em;
  }
  .summary-line.total {
    font-size: .875rem;
    font-weight: 600;
    color: var(--panel-text);
    margin-top: .625rem;
    padding-top: .625rem;
    border-top: 1px solid var(--panel-border);
    margin-bottom: 0;
  }

  /* footer */
  .summary-footer {
    margin-top: auto;
    padding-top: 2.5rem;
  }
  .powered-by {
    display: flex;
    align-items: center;
    gap: .5rem;
    font-size: .75rem;
    color: var(--panel-muted);
    letter-spacing: -.005em;
  }
  .op-icon { width: 16px; height: 16px; display: block; flex-shrink: 0; }
  .powered-by strong { color: var(--panel-text); font-weight: 500; }

  /* ── Form panel — white right ─────────────────────────────────────────────── */
  .form-panel {
    background: #fff;
    display: flex;
    justify-content: flex-start;
    height: 100%;
    overflow-y: auto;
  }
  .form-inner {
    max-width: 420px;
    width: 100%;
    padding: 4rem 2.5rem 3rem 3.5rem;
  }

  /* ── Sections ────────────────────────────────────────────────────────────── */
  .form-section { margin-bottom: 1.75rem; }
  .section-label {
    font-size: .6875rem;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: .1em;
    color: var(--zinc-400);
    margin-bottom: .875rem;
  }

  /* ── Fields & Inputs ──────────────────────────────────────────────────────── */
  .field { margin-bottom: .875rem; }
  .field label {
    display: flex;
    align-items: center;
    gap: .3rem;
    font-size: .8125rem;
    font-weight: 400;
    color: var(--zinc-500);
    margin-bottom: .375rem;
    letter-spacing: -.005em;
  }
  .field input {
    width: 100%;
    padding: .5625rem .75rem;
    border: 1px solid var(--zinc-200);
    border-radius: var(--radius-sm);
    font-size: .9375rem;
    font-family: var(--font);
    color: var(--zinc-950);
    background: #fff;
    outline: none;
    transition: border-color .12s ease, box-shadow .12s ease;
    appearance: none;
    letter-spacing: -.01em;
  }
  .field input::placeholder { color: var(--zinc-400); }
  .field input:hover:not(:focus) { border-color: var(--zinc-300); }
  .field input:focus {
    border-color: var(--zinc-700);
    box-shadow: 0 0 0 3px var(--input-ring);
  }

  /* ── Stripe-style card group ─────────────────────────────────────────────── */
  .card-group {
    border: 1px solid var(--zinc-200);
    border-radius: var(--radius-sm);
    overflow: hidden;
    transition: border-color .12s ease, box-shadow .12s ease;
  }
  .card-group.cg-focused {
    border-color: var(--zinc-700);
    box-shadow: 0 0 0 3px var(--input-ring);
  }
  .cg-row {
    display: flex;
    align-items: stretch;
  }
  .cg-row + .cg-row {
    border-top: 1px solid var(--zinc-200);
  }
  /* inputs inside the group reset all field styles */
  .card-group input {
    border: none !important;
    border-radius: 0 !important;
    box-shadow: none !important;
    flex: 1;
    min-width: 0;
    padding: .5625rem .75rem;
    font-size: .9375rem;
    font-family: var(--font);
    color: var(--zinc-950);
    background: #fff;
    outline: none;
    appearance: none;
    letter-spacing: -.01em;
    width: 100%;
  }
  .card-group input::placeholder { color: var(--zinc-400); }
  /* card brand icons row */
  .ci-group {
    display: flex;
    align-items: center;
    gap: .25rem;
    padding-right: .75rem;
    flex-shrink: 0;
  }
  .ci {
    width: 28px;
    height: 18px;
    display: block;
    transition: opacity .15s ease;
  }
  .ci-dim { opacity: .2; }
  /* vertical separator between expiry and CVC */
  .cg-sep {
    width: 1px;
    background: var(--zinc-200);
    flex-shrink: 0;
    align-self: stretch;
  }

  /* ── Error banner ────────────────────────────────────────────────────────── */
  .error-banner {
    display: flex;
    align-items: flex-start;
    gap: .5rem;
    background: var(--error-bg);
    border: 1px solid var(--error-border);
    border-radius: var(--radius-sm);
    color: var(--error);
    font-size: .8125rem;
    line-height: 1.5;
    padding: .75rem .875rem;
    margin-bottom: 1.125rem;
  }
  .error-banner svg { flex-shrink: 0; margin-top: 1px; }

  /* ── Subscribe button ────────────────────────────────────────────────────── */
  .btn-subscribe {
    width: 100%;
    background: var(--btn-bg);
    color: var(--btn-text);
    border: none;
    border-radius: var(--radius-sm);
    padding: .6875rem 1.25rem;
    font-size: .9375rem;
    font-weight: 500;
    font-family: var(--font);
    letter-spacing: -.01em;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: .5rem;
    transition: background .12s ease, opacity .12s ease, transform .08s ease;
    margin-bottom: 1rem;
    line-height: 1.5;
  }
  .btn-subscribe:hover:not(:disabled) { background: var(--btn-hover); }
  .btn-subscribe:active:not(:disabled) { transform: scale(.99); }
  .btn-subscribe:disabled { opacity: .5; cursor: not-allowed; }

  /* spinner */
  .spinner {
    width: 14px; height: 14px;
    border: 1.5px solid rgba(255,255,255,.3);
    border-top-color: #fff;
    border-radius: 50%;
    animation: spin .65s linear infinite;
    flex-shrink: 0;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  /* ── Terms ───────────────────────────────────────────────────────────────── */
  .terms {
    font-size: .75rem;
    color: var(--zinc-400);
    line-height: 1.6;
    text-align: center;
    margin-bottom: 1.5rem;
    letter-spacing: -.005em;
  }

  /* powered-by mobile duplicate */
  .powered-by-mobile {
    display: none;
    align-items: center;
    justify-content: center;
    gap: .4rem;
    font-size: .75rem;
    color: var(--zinc-400);
  }
  .powered-by-mobile strong { color: var(--zinc-600); font-weight: 500; }

  /* ── Solo states (completed / expired / success / 404) ──────────────────── */
  .solo-wrap {
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 2rem;
    background: var(--zinc-50);
  }
  .solo-card {
    background: #fff;
    border: 1px solid var(--zinc-200);
    border-radius: var(--radius-lg);
    padding: 2.5rem 2rem;
    max-width: 400px;
    width: 100%;
    text-align: center;
  }
  .check-icon { display: inline-block; margin-bottom: 1.125rem; }
  .check-icon svg { width: 48px; height: 48px; display: block; }
  .solo-card h2 {
    font-size: 1.125rem;
    font-weight: 600;
    letter-spacing: -.02em;
    margin-bottom: .5rem;
    color: var(--zinc-900);
  }
  .solo-card p {
    color: var(--zinc-500);
    font-size: .875rem;
    line-height: 1.65;
    margin-bottom: .4rem;
  }
  .sub-detail {
    font-size: .875rem;
    font-weight: 500;
    color: var(--brand) !important;
    margin-top: .5rem;
  }

  /* ── Responsive ──────────────────────────────────────────────────────────── */
  @media (max-width: 768px) {
    .layout {
      grid-template-columns: 1fr;
      grid-template-rows: auto 1fr;
    }
    .summary {
      box-shadow: 0 4px 16px rgba(0,0,0,.06);
      border-bottom: 1px solid var(--panel-border);
      justify-content: center;
    }
    .summary-inner { padding: 2.5rem 1.5rem 2rem; max-width: 100%; }
    .plan-name  { font-size: 1.125rem; }
    .price-amount { font-size: 2.25rem; }
    .summary-footer { display: none; }
    .form-panel { justify-content: center; }
    .form-inner { padding: 2rem 1.5rem; max-width: 100%; }
    .powered-by-mobile { display: flex; }
  }
</style>
