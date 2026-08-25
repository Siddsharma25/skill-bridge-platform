import { Module } from '@nestjs/common';
import { DbModule } from '../db/db.module';
import { NotificationsService } from './notifications.service';

@Module({
  imports: [DbModule],
  providers: [NotificationsService],
  exports: [NotificationsService],
})
export class NotificationsModule {}
