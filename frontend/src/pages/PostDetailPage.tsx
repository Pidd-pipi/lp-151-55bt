import { useEffect, useState } from 'react'
import { Card, Space, Typography, Button, Input, List, message, Tag, Avatar, Empty } from 'antd'
import { LikeOutlined, CommentOutlined, EyeOutlined } from '@ant-design/icons'
import { useParams, useNavigate } from 'react-router-dom'
import { request } from '../api/client'
import type { Comment, PageResult, Post } from '../types'
import { getIdentity } from '../utils/storage'
import WithdrawButton from '../components/WithdrawButton'

export default function PostDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [post, setPost] = useState<Post | null>(null)
  const [comments, setComments] = useState<Comment[]>([])
  const [commentText, setCommentText] = useState('')
  const [notFound, setNotFound] = useState(false)

  const load = async () => {
    try {
      const postData = await request<Post>('get', `/posts/${id}`)
      setPost(postData)
      setNotFound(false)
      const commentData = await request<PageResult<Comment>>('get', `/posts/${id}/comments`, { page: 1, page_size: 20 })
      setComments(commentData.items)
    } catch (e) {
      const status = (e as { response?: { status?: number } }).response?.status
      if (status === 404) {
        setNotFound(true)
        setPost(null)
      } else {
        message.error((e as Error).message)
      }
    }
  }

  useEffect(() => {
    load()
  }, [id])

  const likePost = async () => {
    if (!getIdentity()) {
      message.warning('请先创建匿名身份')
      return
    }
    try {
      await request('post', '/likes/toggle', { targetType: 'post', targetId: Number(id) })
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const likeComment = async (commentId: number) => {
    if (!getIdentity()) {
      message.warning('请先创建匿名身份')
      return
    }
    try {
      await request('post', '/likes/toggle', { targetType: 'comment', targetId: commentId })
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const submitComment = async () => {
    if (!getIdentity()) {
      message.warning('请先创建匿名身份')
      return
    }
    if (!commentText.trim()) return
    try {
      await request('post', '/comments', { postId: Number(id), content: commentText.trim() })
      setCommentText('')
      message.success('评论已发布')
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  if (notFound) {
    return (
      <div>
        <Button type="link" onClick={() => navigate(-1)}>返回</Button>
        <Empty description="帖子不存在或已被撤回" style={{ marginTop: 64 }}>
          <Button type="primary" onClick={() => navigate('/')}>回到首页</Button>
        </Empty>
      </div>
    )
  }

  if (!post) return <Typography.Text>加载中...</Typography.Text>

  return (
    <div>
      <Button type="link" onClick={() => navigate(-1)}>返回</Button>
      <Card className="post-card">
        <Space align="start">
          <Avatar size={48} src={post.avatar} />
          <div>
            <Space>
              <Typography.Text strong>{post.nickname}</Typography.Text>
              <Typography.Text type="secondary">{post.createdAt}</Typography.Text>
            </Space>
            {post.title && <Typography.Title level={4} style={{ margin: '8px 0' }}>{post.title}</Typography.Title>}
            <Typography.Paragraph style={{ whiteSpace: 'pre-wrap' }}>{post.content}</Typography.Paragraph>
            {post.images?.length > 0 && (
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', margin: '8px 0' }}>
                {post.images.map((url, idx) => (
                  <img key={idx} src={url} alt={`${post.nickname}-${idx}`} style={{ width: 160, height: 160, objectFit: 'cover', borderRadius: 8 }} />
                ))}
              </div>
            )}
            {post.tags.map((tag) => (
              <Tag key={tag.id} color="blue">{tag.name}</Tag>
            ))}
            <Space size="large" style={{ marginTop: 8 }}>
              <Button type={post.liked ? 'primary' : 'text'} icon={<LikeOutlined />} onClick={likePost}>
                {post.likeCount}
              </Button>
              <Typography.Text type="secondary"><CommentOutlined /> {post.commentCount}</Typography.Text>
              <Typography.Text type="secondary"><EyeOutlined /> {post.viewCount}</Typography.Text>
              <WithdrawButton
                authorIdentityId={post.identityId}
                url={`/posts/${post.id}/withdraw`}
                title="撤回这条帖子？"
                description="撤回后帖子将从最新、热度、精选和标签列表消失，详情也无法再访问，且无法恢复。"
                size="middle"
                onWithdrawn={() => navigate('/')}
              />
            </Space>
          </div>
        </Space>
      </Card>

      <Card title={`评论 (${comments.length})`} style={{ marginTop: 16 }}>
        <Space.Compact style={{ width: '100%', marginBottom: 16 }}>
          <Input.TextArea value={commentText} onChange={(e) => setCommentText(e.target.value)} placeholder="写下你的匿名评论..." autoSize={{ minRows: 2, maxRows: 4 }} />
        </Space.Compact>
        <Button type="primary" onClick={submitComment} style={{ marginTop: 8 }}>发表评论</Button>
        <List
          style={{ marginTop: 16 }}
          dataSource={comments}
          locale={{ emptyText: '暂无评论' }}
          renderItem={(item) => (
            <List.Item
              actions={[
                <Button key="like" type="text" icon={<LikeOutlined />} onClick={() => likeComment(item.id)}>
                  {item.likeCount}
                </Button>,
                <WithdrawButton
                  key="withdraw"
                  authorIdentityId={item.identityId}
                  url={`/comments/${item.id}/withdraw`}
                  title="撤回这条评论？"
                  description="撤回后评论将从楼层消失，帖子评论数也会减少，且无法恢复。"
                  onWithdrawn={load}
                />,
              ]}
            >
              <List.Item.Meta
                avatar={<Avatar src={item.avatar} />}
                title={<Space><Typography.Text strong>{item.nickname}</Typography.Text><Typography.Text type="secondary">{item.createdAt}</Typography.Text></Space>}
                description={<Typography.Paragraph style={{ marginBottom: 0 }}>{item.content}</Typography.Paragraph>}
              />
            </List.Item>
          )}
        />
      </Card>
    </div>
  )
}
