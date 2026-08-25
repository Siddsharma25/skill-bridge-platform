import { Module } from '@nestjs/common';
import { NotificationsModule } from '../notifications/notifications.module';
import { RabbitmqConnectionService } from './rabbitmq-connection.service';
import { RabbitmqConsumer } from './rabbitmq.consumer';

@Module({
  imports: [NotificationsModule],
  providers: [RabbitmqConnectionService, RabbitmqConsumer],
  exports: [RabbitmqConnectionService],
})
export class RabbitmqModule {}
