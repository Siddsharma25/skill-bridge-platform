import { NotificationsService } from './notifications.service';
import type { PgService } from '../db/pg.service';
import type { PinoLogger } from 'nestjs-pino';
import type { EmailNotification } from '../rabbitmq/email-notification';

function makeSilentLogger(): PinoLogger {
  return {
    warn: jest.fn(),
    error: jest.fn(),
    info: jest.fn(),
    debug: jest.fn(),
    trace: jest.fn(),
    fatal: jest.fn(),
  } as unknown as PinoLogger;
}

const payload: EmailNotification = {
  message_id: 'm-1',
  user_id: 'u-1',
  email: 'a@example.com',
  event_type: 'welcome',
  created_at: '2026-08-25T00:00:00.000Z',
};

describe('NotificationsService.recordWelcomeEmail', () => {
  it('returns db_unavailable and does not throw when the pool is not configured', async () => {
    const pg = { enabled: false, pool: null } as unknown as PgService;
    const service = new NotificationsService(pg, makeSilentLogger());

    const result = await service.recordWelcomeEmail(payload);
    expect(result).toBe('db_unavailable');
  });

  it('returns inserted on a fresh row', async () => {
    const query = jest
      .fn()
      .mockResolvedValue({ rowCount: 1, rows: [{ id: 'row-1' }] });
    const pg = { enabled: true, pool: { query } } as unknown as PgService;
    const service = new NotificationsService(pg, makeSilentLogger());

    const result = await service.recordWelcomeEmail(payload);
    expect(result).toBe('inserted');
    expect(query).toHaveBeenCalledWith(
      expect.stringContaining('ON CONFLICT (message_id) DO NOTHING'),
      [payload.message_id, payload.user_id, payload.email, payload.event_type],
    );
  });

  it('returns duplicate when ON CONFLICT DO NOTHING skips the insert (rowCount 0)', async () => {
    const query = jest.fn().mockResolvedValue({ rowCount: 0, rows: [] });
    const pg = { enabled: true, pool: { query } } as unknown as PgService;
    const service = new NotificationsService(pg, makeSilentLogger());

    const result = await service.recordWelcomeEmail(payload);
    expect(result).toBe('duplicate');
  });

  it('returns db_unavailable (not a thrown error) when the query itself fails', async () => {
    const query = jest.fn().mockRejectedValue(new Error('connection reset'));
    const pg = { enabled: true, pool: { query } } as unknown as PgService;
    const service = new NotificationsService(pg, makeSilentLogger());

    await expect(service.recordWelcomeEmail(payload)).resolves.toBe(
      'db_unavailable',
    );
  });
});
