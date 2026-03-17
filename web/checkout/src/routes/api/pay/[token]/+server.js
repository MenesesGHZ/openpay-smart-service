import { json } from '@sveltejs/kit';
import { API_BASE_URL } from '$env/static/private';

export async function POST({ params, request }) {
  const body = await request.json();

  const res = await fetch(`${API_BASE_URL}/v1/payment-links/${params.token}/redeem`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });

  const data = await res.json().catch(() => ({}));
  return json(data, { status: res.ok ? 200 : res.status });
}
