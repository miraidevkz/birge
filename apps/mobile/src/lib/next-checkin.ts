// When the next weekly check-in reminder is due, from the schedule the
// wellbeing API returns (ISO weekday 1 = Monday, local hour).
//
// The contract's hour is in the deployment's time zone; one deployment serves
// one country, and we assume the device is in that zone. A device abroad
// shows the time shifted, which is acceptable for a reminder.
import type { Schemas } from '@birge/api';

/** nextCheckin returns the next reminder time after `now`, or null when reminders are off or not set. */
export function nextCheckin(schedule: Schemas['CheckinSchedule'] | undefined, now: Date): Date | null {
  if (!schedule || schedule.enabled === false) return null;
  const { weekday, hour } = schedule;
  if (weekday === undefined || hour === undefined) return null;
  const jsDay = weekday % 7; // ISO 7 (Sunday) is JS 0
  const next = new Date(now);
  next.setHours(hour, 0, 0, 0);
  let days = (jsDay - now.getDay() + 7) % 7;
  if (days === 0 && next <= now) days = 7;
  next.setDate(next.getDate() + days);
  return next;
}
