// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Time-based one-time passwords (RFC 6238) on top of HOTP (RFC 4226), using
// only node:crypto. The parameters are the ones Scanopsis uses: HMAC-SHA1,
// 6 digits, 30 second steps.

import { createHmac } from 'node:crypto';

const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
export const stepSeconds = 30;

/** Decodes unpadded or padded RFC 4648 base32, ignoring case and spaces. */
export function base32Decode(text: string): Buffer {
  const clean = text.replace(/[\s=]/g, '').toUpperCase();
  const bytes: number[] = [];
  let bits = 0;
  let value = 0;
  for (const char of clean) {
    const index = alphabet.indexOf(char);
    if (index < 0) throw new Error(`invalid base32 character ${JSON.stringify(char)}`);
    value = (value << 5) | index;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      bytes.push((value >> bits) & 0xff);
    }
  }
  return Buffer.from(bytes);
}

/** HOTP value for a counter (RFC 4226, section 5.3). */
export function hotp(key: Buffer, counter: number, digits = 6): string {
  const message = Buffer.alloc(8);
  message.writeBigUInt64BE(BigInt(counter));
  const mac = createHmac('sha1', key).update(message).digest();
  const offset = mac[mac.length - 1] & 0x0f;
  const binary =
    ((mac[offset] & 0x7f) << 24) |
    (mac[offset + 1] << 16) |
    (mac[offset + 2] << 8) |
    mac[offset + 3];
  return String(binary % 10 ** digits).padStart(digits, '0');
}

/** TOTP value of a raw key at a Unix time in seconds. */
export function totpFromKey(key: Buffer, unixSeconds: number, digits = 6): string {
  return hotp(key, Math.floor(unixSeconds / stepSeconds), digits);
}

/** TOTP value of a base32 secret at a Unix time in seconds. */
export function totp(secret: string, unixSeconds: number): string {
  return totpFromKey(base32Decode(secret), unixSeconds);
}

const lastStep = new Map<string, number>();

/**
 * A code for the current time step that has not been handed out before for
 * this secret. A server accepts each step once, so when the current step was
 * already used this waits for the next one, the only wait the suite makes.
 */
export async function freshCode(secret: string): Promise<string> {
  const stepMs = stepSeconds * 1000;
  let step = Math.floor(Date.now() / stepMs);
  const used = lastStep.get(secret);
  if (used !== undefined && step <= used) {
    const wait = (used + 1) * stepMs - Date.now() + 250;
    await new Promise((resolve) => setTimeout(resolve, wait));
    step = Math.floor(Date.now() / stepMs);
  }
  lastStep.set(secret, step);
  return hotp(base32Decode(secret), step);
}
