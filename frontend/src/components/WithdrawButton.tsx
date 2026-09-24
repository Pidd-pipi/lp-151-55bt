import { useState } from 'react'
import { Button, Modal, Popconfirm, message } from 'antd'
import { DeleteOutlined } from '@ant-design/icons'
import { request, extractError } from '../api/client'
import { getIdentity } from '../utils/storage'

interface WithdrawButtonProps {
  /** 作者身份 ID，与当前本地身份一致时才展示按钮 */
  authorIdentityId: number
  /** 撤回接口地址，如 /posts/12/withdraw */
  url: string
  /** 确认弹窗文案 */
  title: string
  description?: string
  /** 撤回成功后的回调（刷新列表、跳转等） */
  onWithdrawn: () => void
  size?: 'small' | 'middle'
}

// WithdrawButton 仅在当前匿名身份为作者本人时渲染撤回入口，二次确认后展示处理结果。
export default function WithdrawButton({
  authorIdentityId,
  url,
  title,
  description,
  onWithdrawn,
  size = 'small',
}: WithdrawButtonProps) {
  const [submitting, setSubmitting] = useState(false)
  const identity = getIdentity()
  if (!identity || identity.id !== authorIdentityId) {
    return null
  }

  const doWithdraw = async () => {
    setSubmitting(true)
    try {
      await request('delete', url)
      Modal.success({
        title: '撤回成功',
        content: description ?? '内容已撤回，其他用户将无法再看到。',
        onOk: onWithdrawn,
      })
    } catch (error) {
      const status = (error as { response?: { status?: number } }).response?.status
      if (status === 409) {
        message.info('该内容已撤回，无需重复操作')
      } else if (status === 403) {
        message.warning('只能撤回自己发布的内容')
      } else if (status === 404) {
        message.warning('内容不存在或已被撤回')
      } else {
        message.error(extractError(error))
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Popconfirm
      title={title}
      description={description ?? '撤回后内容将从列表与楼层消失，且无法恢复。'}
      okText="确认撤回"
      cancelText="再想想"
      okButtonProps={{ danger: true, loading: submitting }}
      onConfirm={doWithdraw}
    >
      <Button size={size} danger type="text" icon={<DeleteOutlined />} loading={submitting}>
        撤回
      </Button>
    </Popconfirm>
  )
}
